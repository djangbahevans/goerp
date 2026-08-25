package orm

import (
	"errors"
	"testing"

	"github.com/djangbahevans/goerp/sdk/go/engine"
	"github.com/vmihailenco/msgpack/v5"
)

func dispatchComputedAndDecode(t *testing.T, req computeRequest) computeResponse {
	t.Helper()
	data, err := msgpack.Marshal(req)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	ptr := engine.Allocate(uint32(len(data)))
	engine.WriteMem(ptr, data)

	packed := DispatchComputed(ptr, uint32(len(data)))
	respPtr, respLen := uint32(packed>>32), uint32(packed)

	var resp computeResponse
	if err := msgpack.Unmarshal(engine.ReadMem(respPtr, respLen), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	return resp
}

func TestDispatchComputed_RegisteredFn(t *testing.T) {
	RegisterComputed("test_compute_total", func(ctx ComputeContext, record map[string]any) (any, error) {
		qty, _ := record["quantity"].(int8)
		return int64(qty) * 100, nil
	})

	resp := dispatchComputedAndDecode(t, computeRequest{
		FnName: "test_compute_total",
		Record: map[string]any{"quantity": int8(3)},
	})
	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}
	if resp.Value != int64(300) {
		t.Errorf("Value = %v, want 300", resp.Value)
	}
}

func TestDispatchComputed_UnregisteredFn(t *testing.T) {
	resp := dispatchComputedAndDecode(t, computeRequest{FnName: "test_never_registered"})
	if resp.Error == nil || resp.Error.Code != "orm.compute_fn_not_registered" {
		t.Errorf("Error = %+v, want code orm.compute_fn_not_registered", resp.Error)
	}
}

func TestDispatchComputed_FnReturnsError(t *testing.T) {
	RegisterComputed("test_compute_fails", func(ctx ComputeContext, record map[string]any) (any, error) {
		return nil, errors.New("boom")
	})

	resp := dispatchComputedAndDecode(t, computeRequest{FnName: "test_compute_fails"})
	if resp.Error == nil || resp.Error.Code != "orm.backend_error" {
		t.Errorf("Error = %+v, want code orm.backend_error", resp.Error)
	}
}

func TestDispatchComputed_ContextPassedThrough(t *testing.T) {
	var gotCtx ComputeContext
	RegisterComputed("test_compute_ctx", func(ctx ComputeContext, record map[string]any) (any, error) {
		gotCtx = ctx
		return nil, nil
	})

	dispatchComputedAndDecode(t, computeRequest{FnName: "test_compute_ctx", TenantID: "acme", UserID: "u1", TraceID: "t1"})
	if gotCtx.TenantID != "acme" || gotCtx.UserID != "u1" || gotCtx.TraceID != "t1" {
		t.Errorf("ComputeContext = %+v, want TenantID=acme UserID=u1 TraceID=t1", gotCtx)
	}
}
