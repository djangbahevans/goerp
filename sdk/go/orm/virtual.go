package orm

import (
	abi "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/djangbahevans/goerp/sdk/go/internal/wasmmem"
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

// VirtualListParams carries pagination arguments to a List backend
// function. Domain filtering does not apply to Virtual models.
type VirtualListParams struct {
	Limit  int
	Offset int
}

// VirtualBackend holds the callback functions a module registers for one
// Virtual model. A nil field means that operation isn't implemented: the
// module fails to load if EnableOps declares it, and a call to it returns
// orm.virtual_op_not_implemented.
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

// DispatchVirtualOp decodes an abi.VirtualOpRequest from module memory at
// (ptr, length), routes it to the registered VirtualBackend's matching
// function, and writes back a msgpack-encoded abi.VirtualOpResponse. A
// module exports this as
//
//	//go:wasmexport handle_virtual_op
//	func handleVirtualOp(ptr, length uint32) uint64 { return orm.DispatchVirtualOp(ptr, length) }
func DispatchVirtualOp(ptr, length uint32) uint64 {
	buf := wasmmem.ReadMem(ptr, length)

	var req abi.VirtualOpRequest
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
		return writeVirtualOpResponse(&abi.VirtualOpResponse{Record: record})
	case "list":
		if backend.List == nil {
			return writeVirtualOpNotImplemented(req)
		}
		records, err := backend.List(ctx, VirtualListParams{Limit: req.Limit, Offset: req.Offset})
		if err != nil {
			return writeVirtualOpError("orm.backend_error", err.Error())
		}
		return writeVirtualOpResponse(&abi.VirtualOpResponse{Records: records})
	case "create":
		if backend.Create == nil {
			return writeVirtualOpNotImplemented(req)
		}
		record, err := backend.Create(ctx, req.Record)
		if err != nil {
			return writeVirtualOpError("orm.backend_error", err.Error())
		}
		return writeVirtualOpResponse(&abi.VirtualOpResponse{Record: record})
	case "update":
		if backend.Update == nil {
			return writeVirtualOpNotImplemented(req)
		}
		record, err := backend.Update(ctx, req.ID, req.Record, req.ExpectedEtag)
		if err != nil {
			return writeVirtualOpError("orm.backend_error", err.Error())
		}
		return writeVirtualOpResponse(&abi.VirtualOpResponse{Record: record})
	case "delete":
		if backend.Delete == nil {
			return writeVirtualOpNotImplemented(req)
		}
		if err := backend.Delete(ctx, req.ID, req.ExpectedEtag); err != nil {
			return writeVirtualOpError("orm.backend_error", err.Error())
		}
		return writeVirtualOpResponse(&abi.VirtualOpResponse{})
	default:
		return writeVirtualOpError("orm.invalid_request", "unknown op "+req.Op)
	}
}

func writeVirtualOpNotImplemented(req abi.VirtualOpRequest) uint64 {
	return writeVirtualOpError("orm.virtual_op_not_implemented", "no "+req.Op+" backend function registered for model "+req.Model)
}

func writeVirtualOpError(code, message string) uint64 {
	return writeVirtualOpResponse(&abi.VirtualOpResponse{Error: &abi.VirtualOpError{Code: code, Message: message}})
}

func writeVirtualOpResponse(resp *abi.VirtualOpResponse) uint64 {
	data, err := msgpack.Marshal(resp)
	if err != nil {
		data, _ = msgpack.Marshal(&abi.VirtualOpResponse{
			Error: &abi.VirtualOpError{Code: "orm.marshal_failed", Message: err.Error()},
		})
	}
	ptr := wasmmem.Allocate(uint32(len(data)))
	wasmmem.WriteMem(ptr, data)
	return uint64(ptr)<<32 | uint64(len(data))
}

// WriteVirtualBackendDescriptors msgpack-encodes, per registered model,
// which ops have a backend function; the engine checks EnableOps against
// it at load time. A module exports this as
//
//	//go:wasmexport get_virtual_backends
//	func getVirtualBackends() uint64 { return orm.WriteVirtualBackendDescriptors() }
//
// only if it registers at least one Virtual backend.
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
	ptr := wasmmem.Allocate(uint32(len(data)))
	wasmmem.WriteMem(ptr, data)
	return uint64(ptr)<<32 | uint64(len(data))
}
