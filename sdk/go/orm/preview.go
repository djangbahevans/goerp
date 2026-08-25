package orm

import (
	"github.com/djangbahevans/goerp/sdk/go/engine"
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
// Store(true)/.Depends() declaration can express — go-sdk-reference.md
// §22 "Preview action", "Escape hatch for logic beyond field
// dependencies". This is a genuinely optional escape hatch, not a
// requirement: the engine already recomputes every Store(true)/.Depends()
// field whose dependencies are present in the draft before ever calling a
// registered hook (internal/engine/wasm/host_orm_preview.go) — most
// models need no PreviewHook at all.
type PreviewHook func(ctx PreviewContext, draft map[string]any) map[string]any

var previewRegistry = map[string]PreviewHook{}

// RegisterPreviewHook associates modelName (module-qualified, e.g.
// "sales.order") with the hook that runs against its preview draft after
// the engine's own .Depends() recompute pass. Call from init().
func RegisterPreviewHook(modelName string, hook PreviewHook) {
	previewRegistry[modelName] = hook
}

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

// DispatchPreview decodes a previewRequest from module memory at (ptr,
// length), routes it to the PreviewHook registered for req.Model, and
// writes back a msgpack-encoded previewResponse — the same decode/route/
// encode shape orm.DispatchComputed (computed.go) already uses. A model
// with no registered hook passes the draft through unchanged rather than
// erroring — "no hook" is the expected common case, not a caller
// mistake, unlike orm.DispatchVirtualOp's virtual_op_not_implemented
// (Virtual models require a backend function per declared op). A module
// exports this as
//
//	//go:wasmexport handle_orm_preview
//	func handleOrmPreview(ptr, length uint32) uint64 { return orm.DispatchPreview(ptr, length) }
func DispatchPreview(ptr, length uint32) uint64 {
	buf := engine.ReadMem(ptr, length)

	var req previewRequest
	if err := msgpack.Unmarshal(buf, &req); err != nil {
		return writePreviewResponse(&previewResponse{Error: &previewError{Code: "orm.invalid_request", Message: err.Error()}})
	}

	hook, ok := previewRegistry[req.Model]
	if !ok {
		return writePreviewResponse(&previewResponse{Record: req.Record})
	}

	ctx := PreviewContext{TenantID: req.TenantID, UserID: req.UserID, TraceID: req.TraceID}
	return writePreviewResponse(&previewResponse{Record: hook(ctx, req.Record)})
}

func writePreviewResponse(resp *previewResponse) uint64 {
	data, err := msgpack.Marshal(resp)
	if err != nil {
		data, _ = msgpack.Marshal(&previewResponse{
			Error: &previewError{Code: "orm.marshal_failed", Message: err.Error()},
		})
	}
	ptr := engine.Allocate(uint32(len(data)))
	engine.WriteMem(ptr, data)
	return uint64(ptr)<<32 | uint64(len(data))
}
