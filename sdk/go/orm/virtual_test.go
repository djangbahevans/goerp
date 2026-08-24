package orm

import (
	"errors"
	"testing"

	"github.com/djangbahevans/goerp/sdk/go/engine"
	"github.com/vmihailenco/msgpack/v5"
)

func dispatchAndDecode(t *testing.T, req virtualOpRequest) virtualOpResponse {
	t.Helper()
	data, err := msgpack.Marshal(req)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	ptr := engine.Allocate(uint32(len(data)))
	engine.WriteMem(ptr, data)

	packed := DispatchVirtualOp(ptr, uint32(len(data)))
	respPtr, respLen := uint32(packed>>32), uint32(packed)

	var resp virtualOpResponse
	if err := msgpack.Unmarshal(engine.ReadMem(respPtr, respLen), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	return resp
}

func TestDispatchVirtualOp_RegisteredRead(t *testing.T) {
	RegisterVirtualBackend("test.read_model", VirtualBackend{
		Read: func(ctx VirtualContext, id string) (map[string]any, error) {
			return map[string]any{"id": id, "tenant": ctx.TenantID}, nil
		},
	})

	resp := dispatchAndDecode(t, virtualOpRequest{Model: "test.read_model", Op: "read", ID: "42", TenantID: "acme"})
	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}
	if resp.Record["id"] != "42" || resp.Record["tenant"] != "acme" {
		t.Errorf("Record = %+v, want id=42 tenant=acme", resp.Record)
	}
}

func TestDispatchVirtualOp_UnregisteredOpOnRegisteredModel(t *testing.T) {
	RegisterVirtualBackend("test.read_only_model", VirtualBackend{
		Read: func(ctx VirtualContext, id string) (map[string]any, error) {
			return map[string]any{"id": id}, nil
		},
	})

	resp := dispatchAndDecode(t, virtualOpRequest{Model: "test.read_only_model", Op: "create"})
	if resp.Error == nil || resp.Error.Code != "orm.virtual_op_not_implemented" {
		t.Errorf("Error = %+v, want code orm.virtual_op_not_implemented", resp.Error)
	}
}

func TestDispatchVirtualOp_UnregisteredModel(t *testing.T) {
	resp := dispatchAndDecode(t, virtualOpRequest{Model: "test.never_registered", Op: "read", ID: "1"})
	if resp.Error == nil || resp.Error.Code != "orm.virtual_op_not_implemented" {
		t.Errorf("Error = %+v, want code orm.virtual_op_not_implemented", resp.Error)
	}
}

func TestDispatchVirtualOp_BackendErrorSurfaces(t *testing.T) {
	RegisterVirtualBackend("test.erroring_model", VirtualBackend{
		Read: func(ctx VirtualContext, id string) (map[string]any, error) {
			return nil, errors.New("upstream unavailable")
		},
	})

	resp := dispatchAndDecode(t, virtualOpRequest{Model: "test.erroring_model", Op: "read", ID: "1"})
	if resp.Error == nil || resp.Error.Code != "orm.backend_error" || resp.Error.Message != "upstream unavailable" {
		t.Errorf("Error = %+v, want backend_error/upstream unavailable", resp.Error)
	}
}

func TestWriteVirtualBackendDescriptors_ReflectsRegisteredOps(t *testing.T) {
	RegisterVirtualBackend("test.descriptor_model", VirtualBackend{
		Read: func(ctx VirtualContext, id string) (map[string]any, error) { return nil, nil },
		List: func(ctx VirtualContext, params VirtualListParams) ([]map[string]any, error) { return nil, nil },
	})

	packed := WriteVirtualBackendDescriptors()
	ptr, length := uint32(packed>>32), uint32(packed)

	var descriptors map[string][]string
	if err := msgpack.Unmarshal(engine.ReadMem(ptr, length), &descriptors); err != nil {
		t.Fatalf("unmarshal descriptors: %v", err)
	}

	ops := descriptors["test.descriptor_model"]
	if len(ops) != 2 {
		t.Fatalf("got %d ops, want 2: %v", len(ops), ops)
	}
	hasRead, hasList := false, false
	for _, op := range ops {
		switch op {
		case "read":
			hasRead = true
		case "list":
			hasList = true
		}
	}
	if !hasRead || !hasList {
		t.Errorf("ops = %v, want [read list]", ops)
	}
}
