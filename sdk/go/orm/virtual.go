// Package orm holds the module-side runtime a connector module's own
// init() registers against — RegisterVirtualBackend and the dispatch
// entry point the engine calls into for a Virtual-backed model
// (go-sdk-reference.md §22 "Virtual models"). This is distinct from
// internal/engine/orm, which never compiles into a module's WASM binary.
package orm

import (
	"github.com/djangbahevans/goerp/sdk/go/engine"
	"github.com/vmihailenco/msgpack/v5"
)

// VirtualContext carries the request-scoped identity a Virtual backend
// function needs to make its own host calls (e.g. http.Fetch,
// host.cache) — the same information a request handler already has.
type VirtualContext struct {
	TenantID string
	UserID   string
	TraceID  string
}

// VirtualListParams carries search's pagination arguments through to a
// List backend function — filtering/domain compilation doesn't apply
// here the way it does for a Table-backed model (a Virtual model's List
// route can't declare an ABAC condition at all — see EnableOps(List)'s
// load-time rejection in internal/engine/loader).
type VirtualListParams struct {
	Limit  int
	Offset int
}

// VirtualBackend holds the callback functions a module registers for one
// Virtual model. A nil field means that operation isn't implemented —
// declaring EnableOps for an op with no matching backend function here is
// a load-time error (internal/engine/loader); calling an op that was
// never declared in EnableOps at all but also has no backend function
// returns orm.virtual_op_not_implemented at dispatch time.
type VirtualBackend struct {
	Read   func(ctx VirtualContext, id string) (map[string]any, error)
	List   func(ctx VirtualContext, params VirtualListParams) ([]map[string]any, error)
	Create func(ctx VirtualContext, record map[string]any) (map[string]any, error)
	Update func(ctx VirtualContext, id string, record map[string]any, expectedEtag string) (map[string]any, error)
	Delete func(ctx VirtualContext, id string, expectedEtag string) error
}

var registry = map[string]VirtualBackend{}

// RegisterVirtualBackend associates modelName (module-qualified, e.g.
// "legacy.inventory_item") with the backend functions that serve its
// host.orm calls. Call from init().
func RegisterVirtualBackend(modelName string, backend VirtualBackend) {
	registry[modelName] = backend
}

type virtualOpRequest struct {
	Model        string         `msgpack:"model"`
	Op           string         `msgpack:"op"`
	ID           string         `msgpack:"id,omitempty"`
	Record       map[string]any `msgpack:"record,omitempty"`
	ExpectedEtag string         `msgpack:"expected_etag,omitempty"`
	Limit        int            `msgpack:"limit,omitempty"`
	Offset       int            `msgpack:"offset,omitempty"`
	TenantID     string         `msgpack:"tenant_id,omitempty"`
	UserID       string         `msgpack:"user_id,omitempty"`
	TraceID      string         `msgpack:"trace_id,omitempty"`
}

type virtualOpResponse struct {
	Record  map[string]any   `msgpack:"record,omitempty"`
	Records []map[string]any `msgpack:"records,omitempty"`
	Error   *virtualOpError  `msgpack:"error,omitempty"`
}

type virtualOpError struct {
	Code    string `msgpack:"code"`
	Message string `msgpack:"message"`
}

// DispatchVirtualOp decodes a virtualOpRequest from module memory at
// (ptr, length), routes it to the registered VirtualBackend's matching
// function, and writes back a msgpack-encoded virtualOpResponse — the
// same decode/route/encode shape sdk/go/engine.DispatchRequest already
// uses for handle_request, just routing through the registry above
// instead of DefaultRouter. A module exports this as
//
//	//go:wasmexport handle_virtual_op
//	func handleVirtualOp(ptr, length uint32) uint64 { return orm.DispatchVirtualOp(ptr, length) }
func DispatchVirtualOp(ptr, length uint32) uint64 {
	buf := engine.ReadMem(ptr, length)

	var req virtualOpRequest
	if err := msgpack.Unmarshal(buf, &req); err != nil {
		return writeVirtualOpError("orm.invalid_request", err.Error())
	}

	backend, ok := registry[req.Model]
	if !ok {
		return writeVirtualOpError("orm.virtual_op_not_implemented", "no Virtual backend registered for model "+req.Model)
	}

	ctx := VirtualContext{TenantID: req.TenantID, UserID: req.UserID, TraceID: req.TraceID}

	switch req.Op {
	case "read":
		if backend.Read == nil {
			return writeVirtualOpNotImplemented(req)
		}
		record, err := backend.Read(ctx, req.ID)
		if err != nil {
			return writeVirtualOpError("orm.backend_error", err.Error())
		}
		return writeVirtualOpResponse(&virtualOpResponse{Record: record})
	case "list":
		if backend.List == nil {
			return writeVirtualOpNotImplemented(req)
		}
		records, err := backend.List(ctx, VirtualListParams{Limit: req.Limit, Offset: req.Offset})
		if err != nil {
			return writeVirtualOpError("orm.backend_error", err.Error())
		}
		return writeVirtualOpResponse(&virtualOpResponse{Records: records})
	case "create":
		if backend.Create == nil {
			return writeVirtualOpNotImplemented(req)
		}
		record, err := backend.Create(ctx, req.Record)
		if err != nil {
			return writeVirtualOpError("orm.backend_error", err.Error())
		}
		return writeVirtualOpResponse(&virtualOpResponse{Record: record})
	case "update":
		if backend.Update == nil {
			return writeVirtualOpNotImplemented(req)
		}
		record, err := backend.Update(ctx, req.ID, req.Record, req.ExpectedEtag)
		if err != nil {
			return writeVirtualOpError("orm.backend_error", err.Error())
		}
		return writeVirtualOpResponse(&virtualOpResponse{Record: record})
	case "delete":
		if backend.Delete == nil {
			return writeVirtualOpNotImplemented(req)
		}
		if err := backend.Delete(ctx, req.ID, req.ExpectedEtag); err != nil {
			return writeVirtualOpError("orm.backend_error", err.Error())
		}
		return writeVirtualOpResponse(&virtualOpResponse{})
	default:
		return writeVirtualOpError("orm.invalid_request", "unknown op "+req.Op)
	}
}

func writeVirtualOpNotImplemented(req virtualOpRequest) uint64 {
	return writeVirtualOpError("orm.virtual_op_not_implemented", "no "+req.Op+" backend function registered for model "+req.Model)
}

func writeVirtualOpError(code, message string) uint64 {
	return writeVirtualOpResponse(&virtualOpResponse{Error: &virtualOpError{Code: code, Message: message}})
}

func writeVirtualOpResponse(resp *virtualOpResponse) uint64 {
	data, err := msgpack.Marshal(resp)
	if err != nil {
		data, _ = msgpack.Marshal(&virtualOpResponse{
			Error: &virtualOpError{Code: "orm.marshal_failed", Message: err.Error()},
		})
	}
	ptr := engine.Allocate(uint32(len(data)))
	engine.WriteMem(ptr, data)
	return uint64(ptr)<<32 | uint64(len(data))
}

// WriteVirtualBackendDescriptors msgpack-encodes, per registered model,
// which ops have a non-nil backend function — the data
// internal/engine/loader validates EnableOps against at load time (a
// declared op with no matching registered function is a load-time
// error). A module exports this as
//
//	//go:wasmexport get_virtual_backends
//	func getVirtualBackends() uint64 { return orm.WriteVirtualBackendDescriptors() }
//
// only if it registers at least one Virtual backend — the loader calls
// this export conditionally, never for a module with no Virtual model
// declared.
func WriteVirtualBackendDescriptors() uint64 {
	descriptors := make(map[string][]string, len(registry))
	for modelName, backend := range registry {
		var ops []string
		if backend.Read != nil {
			ops = append(ops, "read")
		}
		if backend.List != nil {
			ops = append(ops, "list")
		}
		if backend.Create != nil {
			ops = append(ops, "create")
		}
		if backend.Update != nil {
			ops = append(ops, "update")
		}
		if backend.Delete != nil {
			ops = append(ops, "delete")
		}
		descriptors[modelName] = ops
	}

	data, err := msgpack.Marshal(descriptors)
	if err != nil {
		data, _ = msgpack.Marshal(map[string][]string{})
	}
	ptr := engine.Allocate(uint32(len(data)))
	engine.WriteMem(ptr, data)
	return uint64(ptr)<<32 | uint64(len(data))
}
