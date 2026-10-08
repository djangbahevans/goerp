package orm

import (
	abi "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/djangbahevans/goerp/sdk/go/internal/wasmmem"
	"github.com/vmihailenco/msgpack/v5"
)

// PreviewContext carries the request-scoped identity a preview hook needs
// to make its own host calls (e.g. host.orm.read for related data) — the
// same information a request handler already has.
type PreviewContext struct {
	TenantID string
	UserID   string
	TraceID  string
}

// PreviewHook mutates an in-memory draft for logic beyond what a
// Store(true)/.Depends() declaration can express. It is optional: the
// engine recomputes every Store(true)/.Depends() field whose dependencies
// are in the draft before calling a hook, so most models need none.
type PreviewHook func(ctx PreviewContext, draft map[string]any) map[string]any

var previewRegistry = map[string]PreviewHook{}

// RegisterPreviewHook associates modelName (module-qualified, e.g.
// "sales.order") with the hook that runs against its preview draft after
// the engine's own .Depends() recompute pass. Call from init().
func RegisterPreviewHook(modelName string, hook PreviewHook) {
	previewRegistry[modelName] = hook
}

// DispatchPreview decodes an abi.PreviewRequest from module memory at (ptr,
// length), routes it to the PreviewHook registered for req.Model, and
// writes back a msgpack-encoded abi.PreviewResponse. A model with no
// registered hook passes the draft through unchanged. A module exports
// this as
//
//	//go:wasmexport handle_orm_preview
//	func handleOrmPreview(ptr, length uint32) uint64 { return orm.DispatchPreview(ptr, length) }
func DispatchPreview(ptr, length uint32) uint64 {
	buf := wasmmem.ReadMem(ptr, length)

	var req abi.PreviewRequest
	if err := msgpack.Unmarshal(buf, &req); err != nil {
		return writePreviewResponse(&abi.PreviewResponse{Error: &abi.PreviewError{Code: "orm.invalid_request", Message: err.Error()}})
	}

	hook, ok := previewRegistry[req.Model]
	if !ok {
		return writePreviewResponse(&abi.PreviewResponse{Record: req.Record})
	}

	ctx := PreviewContext{TenantID: req.TenantID, UserID: req.UserID, TraceID: req.TraceID}
	return writePreviewResponse(&abi.PreviewResponse{Record: hook(ctx, req.Record)})
}

func writePreviewResponse(resp *abi.PreviewResponse) uint64 {
	data, err := msgpack.Marshal(resp)
	if err != nil {
		data, _ = msgpack.Marshal(&abi.PreviewResponse{
			Error: &abi.PreviewError{Code: "orm.marshal_failed", Message: err.Error()},
		})
	}
	ptr := wasmmem.Allocate(uint32(len(data)))
	wasmmem.WriteMem(ptr, data)
	return uint64(ptr)<<32 | uint64(len(data))
}
