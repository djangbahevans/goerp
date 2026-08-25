package wasm

import (
	"context"
	"maps"
	"strings"

	"github.com/djangbahevans/goerp/internal/engine/abi"
	"github.com/djangbahevans/goerp/internal/engine/computed"
	"github.com/vmihailenco/msgpack/v5"
)

// previewRequest/previewResponse mirror sdk/go/orm's own unexported
// previewRequest/previewResponse wire shapes by field name and msgpack
// tag (sdk/go/orm/preview.go) — the same cross-boundary mirroring
// computeRequest/computeResponse use (host_orm_compute.go) for the
// identical reason: sdk/go/orm compiles into a module's WASM binary, not
// the engine, so this package can't import its unexported types.
type previewRequest struct {
	Model    string         `msgpack:"model"`
	Record   map[string]any `msgpack:"record"`
	TenantID string         `msgpack:"tenant_id,omitempty"`
	UserID   string         `msgpack:"user_id,omitempty"`
	TraceID  string         `msgpack:"trace_id,omitempty"`
}

type previewResponse struct {
	Record map[string]any `msgpack:"record,omitempty"`
	Error  *previewError  `msgpack:"error,omitempty"`
}

type previewError struct {
	Code    string `msgpack:"code"`
	Message string `msgpack:"message"`
}

// This file holds Preview's dispatch (goerp#372) — not one of host.orm's
// six CRUD operations, so unlike every other file in this package it has
// no makeORMxxx WASM closure or registerHostORM entry: Preview is only
// ever reachable through dispatchORMRoute's EnableOps-derived HTTP route,
// never as a host.orm.* call a module makes itself
// (host-abi-reference.md §5a).

type ORMPreviewInput struct {
	Model  string
	Record map[string]any
}

type ORMPreviewOutput struct {
	Record map[string]any
}

// ORMPreview recomputes every Store(true)/.Computed() field on input.Model
// whose .Depends() dependencies are present in input.Record, then — if
// the model's own module registered one — runs its PreviewHook against
// the result. Nothing is ever written to the database and no
// orm.record.* event fires (go-sdk-reference.md §22 "Preview action").
func ORMPreview(ctx context.Context, r *Runtime, modCtx *ModuleContext, input ORMPreviewInput) (ORMPreviewOutput, *abi.HostError) {
	md, ok := resolveModel(modCtx, input.Model)
	if !ok {
		return ORMPreviewOutput{}, &abi.HostError{Code: abi.ErrCodeModelNotFound, Message: "model " + input.Model + " is not declared by this module"}
	}

	draft := make(map[string]any, len(input.Record))
	maps.Copy(draft, input.Record)

	for _, f := range md.Fields {
		if !f.Def.IsComputed || !f.Def.IsStored {
			continue
		}
		if !dependenciesPresent(f.Def.DependsOn, draft) {
			continue
		}
		dep := computed.Dependent{ModuleName: modCtx.ModuleName, ModelDecl: md, Field: f.Name, ComputeFn: f.Def.ComputeFn}
		value, hostErr := invokeCompute(ctx, r, modCtx, dep, draft)
		if hostErr != nil {
			return ORMPreviewOutput{}, hostErr
		}
		draft[f.Name] = value
	}

	if hostErr := runPreviewHook(ctx, r, modCtx, input.Model, &draft); hostErr != nil {
		return ORMPreviewOutput{}, hostErr
	}

	applyFieldMasking(modCtx, input.Model, []map[string]any{draft})

	return ORMPreviewOutput{Record: draft}, nil
}

// dependenciesPresent reports whether every path in depends resolves to a
// key already present in draft — a bare field name directly, or a dotted
// "relField.remoteField" path checked via relField+"_id" (the same
// resolution computed.Index.Register uses when indexing a Many2One-hop
// dependency, internal/engine/computed/index.go). An empty depends is
// vacuously satisfied.
func dependenciesPresent(depends []string, draft map[string]any) bool {
	for _, path := range depends {
		relField, _, hop := strings.Cut(path, ".")
		key := path
		if hop {
			key = relField + "_id"
		}
		if _, ok := draft[key]; !ok {
			return false
		}
	}
	return true
}

// runPreviewHook invokes modelName's registered PreviewHook (if its
// owning module exports handle_orm_preview at all) against *draft,
// replacing it with the hook's response. A module that never registers a
// preview hook for any model is left untouched — HasHandlePreview is
// checked before ever borrowing an instance, so this is a clean no-op for
// the common case (go-sdk-reference.md §22: "No module code is required
// for the common case").
func runPreviewHook(ctx context.Context, r *Runtime, modCtx *ModuleContext, modelName string, draft *map[string]any) *abi.HostError {
	// A model with no computed fields and no preview hook needs no WASM
	// instance for Preview at all — checking Pool availability first
	// (rather than always borrowing via borrowModuleInstance, which
	// treats a missing pool as a hard error for invokeCompute's own,
	// non-optional use case) keeps the hook itself genuinely optional:
	// a module with no live pool for this request simply has no hook to
	// run, not a failure.
	target, ok := modCtx.ComputeTargets()[modCtx.ModuleName]
	if !ok || target.Pool == nil {
		return nil
	}

	inst, cleanup, hostErr := borrowModuleInstance(ctx, r, modCtx, modCtx.ModuleName)
	if hostErr != nil {
		return hostErr
	}
	defer cleanup()

	if !inst.HasHandlePreview() {
		return nil
	}

	payload, err := msgpack.Marshal(previewRequest{
		Model:    modelName,
		Record:   *draft,
		TenantID: modCtx.TenantID,
		UserID:   modCtx.UserID,
		TraceID:  modCtx.TraceID,
	})
	if err != nil {
		return &abi.HostError{Code: abi.ErrCodeUnavailable, Message: err.Error()}
	}

	respBytes, err := inst.InvokeHandlePreview(ctx, payload)
	if err != nil {
		return &abi.HostError{Code: abi.ErrCodeUnavailable, Message: "preview " + modelName + ": " + err.Error()}
	}

	var resp previewResponse
	if err := msgpack.Unmarshal(respBytes, &resp); err != nil {
		return &abi.HostError{Code: abi.ErrCodeUnavailable, Message: err.Error()}
	}
	if resp.Error != nil {
		return &abi.HostError{Code: resp.Error.Code, Message: resp.Error.Message}
	}
	*draft = resp.Record
	return nil
}
