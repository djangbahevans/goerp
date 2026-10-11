package orm

import (
	"errors"
	"slices"
	"testing"

	abi "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/djangbahevans/goerp/sdk/go/internal/wasmmem"
	"github.com/vmihailenco/msgpack/v5"
)

type virtualReadModel struct{}

func (virtualReadModel) ResourceName() string { return "test.read_model" }

type virtualReadOnlyModel struct{}

func (virtualReadOnlyModel) ResourceName() string { return "test.read_only_model" }

type virtualErroringModel struct{}

func (virtualErroringModel) ResourceName() string { return "test.erroring_model" }

type virtualDescriptorModel struct{}

func (virtualDescriptorModel) ResourceName() string { return "test.descriptor_model" }

func dispatchAndDecode(t *testing.T, req abi.VirtualOpRequest) abi.VirtualOpResponse {
	t.Helper()
	data, err := msgpack.Marshal(req)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	ptr := wasmmem.Allocate(uint32(len(data)))
	wasmmem.WriteMem(ptr, data)

	packed := DispatchVirtualOp(ptr, uint32(len(data)))
	respPtr, respLen := uint32(packed>>32), uint32(packed)

	var resp abi.VirtualOpResponse
	if err := msgpack.Unmarshal(wasmmem.ReadMem(respPtr, respLen), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	return resp
}

func TestDispatchVirtualOp_RegisteredRead(t *testing.T) {
	RegisterVirtualBackend[virtualReadModel](VirtualBackend{
		Read: func(ctx VirtualContext, id string) (map[string]any, error) {
			return map[string]any{"id": id, "tenant": ctx.TenantID}, nil
		},
	})

	resp := dispatchAndDecode(t, abi.VirtualOpRequest{Model: "test.read_model", Op: "read", ID: "42", TenantID: "acme"})
	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}
	if resp.Record["id"] != "42" || resp.Record["tenant"] != "acme" {
		t.Errorf("Record = %+v, want id=42 tenant=acme", resp.Record)
	}
}

func TestDispatchVirtualOp_UnregisteredOpOnRegisteredModel(t *testing.T) {
	RegisterVirtualBackend[virtualReadOnlyModel](VirtualBackend{
		Read: func(ctx VirtualContext, id string) (map[string]any, error) {
			return map[string]any{"id": id}, nil
		},
	})

	resp := dispatchAndDecode(t, abi.VirtualOpRequest{Model: "test.read_only_model", Op: "create"})
	if resp.Error == nil || resp.Error.Code != "orm.virtual_op_not_implemented" {
		t.Errorf("Error = %+v, want code orm.virtual_op_not_implemented", resp.Error)
	}
}

func TestDispatchVirtualOp_UnregisteredModel(t *testing.T) {
	resp := dispatchAndDecode(t, abi.VirtualOpRequest{Model: "test.never_registered", Op: "read", ID: "1"})
	if resp.Error == nil || resp.Error.Code != "orm.virtual_op_not_implemented" {
		t.Errorf("Error = %+v, want code orm.virtual_op_not_implemented", resp.Error)
	}
}

func TestDispatchVirtualOp_BackendErrorSurfaces(t *testing.T) {
	RegisterVirtualBackend[virtualErroringModel](VirtualBackend{
		Read: func(ctx VirtualContext, id string) (map[string]any, error) {
			return nil, errors.New("upstream unavailable")
		},
	})

	resp := dispatchAndDecode(t, abi.VirtualOpRequest{Model: "test.erroring_model", Op: "read", ID: "1"})
	if resp.Error == nil || resp.Error.Code != "orm.backend_error" || resp.Error.Message != "upstream unavailable" {
		t.Errorf("Error = %+v, want backend_error/upstream unavailable", resp.Error)
	}
}

func TestWriteVirtualBackendDescriptors_ReflectsRegisteredOps(t *testing.T) {
	RegisterVirtualBackend[virtualDescriptorModel](VirtualBackend{
		Read: func(ctx VirtualContext, id string) (map[string]any, error) { return nil, nil },
		List: func(ctx VirtualContext, params VirtualListParams) ([]map[string]any, error) { return nil, nil },
	})

	packed := WriteVirtualBackendDescriptors()
	ptr, length := uint32(packed>>32), uint32(packed)

	var descriptors map[string][]string
	if err := msgpack.Unmarshal(wasmmem.ReadMem(ptr, length), &descriptors); err != nil {
		t.Fatalf("unmarshal descriptors: %v", err)
	}

	ops := descriptors["test.descriptor_model"]
	if len(ops) != 2 {
		t.Fatalf("got %d ops, want 2: %v", len(ops), ops)
	}
	if !slices.Contains(ops, "read") || !slices.Contains(ops, "list") {
		t.Errorf("ops = %v, want [read list]", ops)
	}
}
