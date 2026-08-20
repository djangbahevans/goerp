package engine

import (
	"errors"
	"testing"
)

var errNotRetryableForTest = errors.New("database temporarily unavailable")

func TestDispatchActivity_EndToEndSuccess(t *testing.T) {
	withFreshActivityHandlers(t)

	OnActivity("reserve_inventory", func(ctx *ActivityContext, in reserveInput) (reserveOutput, error) {
		if ctx.TenantID != "tenant_1" {
			t.Errorf("ctx.TenantID = %q, want tenant_1", ctx.TenantID)
		}
		return reserveOutput{ReservationID: "res_" + in.OrderID}, nil
	})

	req := ActivityRequest{
		Activity: "reserve_inventory",
		TenantID: "tenant_1",
		Attempt:  1,
	}
	payload, err := marshal(reserveInput{OrderID: "ord_1", Qty: 2})
	if err != nil {
		t.Fatalf("marshal input fixture: %v", err)
	}
	req.Payload = payload

	reqBytes, err := marshal(req)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	ptr := Allocate(uint32(len(reqBytes)))
	WriteMem(ptr, reqBytes)

	packed := DispatchActivity(ptr, uint32(len(reqBytes)))
	resPtr := uint32(packed >> 32)
	resLen := uint32(packed)

	var result ActivityResult
	if err := unmarshal(ReadMem(resPtr, resLen), &result); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if result.Error != "" {
		t.Fatalf("result.Error = %q, want empty", result.Error)
	}

	var output reserveOutput
	if err := unmarshal(result.Output, &output); err != nil {
		t.Fatalf("unmarshal output: %v", err)
	}
	if output.ReservationID != "res_ord_1" {
		t.Fatalf("ReservationID = %q, want res_ord_1", output.ReservationID)
	}
}

func TestDispatchActivity_UnregisteredActivityReturnsError(t *testing.T) {
	withFreshActivityHandlers(t)

	req := ActivityRequest{Activity: "does_not_exist"}
	reqBytes, _ := marshal(req)
	ptr := Allocate(uint32(len(reqBytes)))
	WriteMem(ptr, reqBytes)

	packed := DispatchActivity(ptr, uint32(len(reqBytes)))
	resPtr := uint32(packed >> 32)
	resLen := uint32(packed)

	var result ActivityResult
	if err := unmarshal(ReadMem(resPtr, resLen), &result); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if result.Error == "" {
		t.Fatal("want a non-empty Error for an unregistered activity")
	}
}

func TestDispatchActivity_NonRetryableErrorSetsFlagAndDetails(t *testing.T) {
	withFreshActivityHandlers(t)

	OnActivity("reserve_inventory", func(ctx *ActivityContext, in reserveInput) (reserveOutput, error) {
		return reserveOutput{}, WorkflowApplicationError("inventory.insufficient_stock", map[string]any{"order_id": in.OrderID})
	})

	payload, _ := marshal(reserveInput{OrderID: "ord_9"})
	req := ActivityRequest{Activity: "reserve_inventory", Payload: payload}
	reqBytes, _ := marshal(req)
	ptr := Allocate(uint32(len(reqBytes)))
	WriteMem(ptr, reqBytes)

	packed := DispatchActivity(ptr, uint32(len(reqBytes)))
	resPtr := uint32(packed >> 32)
	resLen := uint32(packed)

	var result ActivityResult
	if err := unmarshal(ReadMem(resPtr, resLen), &result); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if !result.NonRetryable {
		t.Error("NonRetryable = false, want true")
	}
	if result.ErrorType != "inventory.insufficient_stock" {
		t.Errorf("ErrorType = %q, want inventory.insufficient_stock", result.ErrorType)
	}
	if result.ErrorDetails["order_id"] != "ord_9" {
		t.Errorf("ErrorDetails[order_id] = %v, want ord_9", result.ErrorDetails["order_id"])
	}
}

func TestDispatchActivity_TransientErrorLeavesNonRetryableFalse(t *testing.T) {
	withFreshActivityHandlers(t)

	OnActivity("reserve_inventory", func(ctx *ActivityContext, in reserveInput) (reserveOutput, error) {
		return reserveOutput{}, errNotRetryableForTest
	})

	req := ActivityRequest{Activity: "reserve_inventory"}
	reqBytes, _ := marshal(req)
	ptr := Allocate(uint32(len(reqBytes)))
	WriteMem(ptr, reqBytes)

	packed := DispatchActivity(ptr, uint32(len(reqBytes)))
	resPtr := uint32(packed >> 32)
	resLen := uint32(packed)

	var result ActivityResult
	if err := unmarshal(ReadMem(resPtr, resLen), &result); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if result.NonRetryable {
		t.Error("NonRetryable = true, want false for a plain transient error")
	}
	if result.Error == "" {
		t.Error("want a non-empty Error")
	}
}
