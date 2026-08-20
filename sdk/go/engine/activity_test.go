package engine

import (
	"errors"
	"testing"
)

func withFreshActivityHandlers(t *testing.T) {
	t.Helper()
	orig := activityHandlers
	activityHandlers = map[string]activityHandler{}
	t.Cleanup(func() { activityHandlers = orig })
}

type reserveInput struct {
	OrderID string `msgpack:"order_id"`
	Qty     int    `msgpack:"qty"`
}

type reserveOutput struct {
	ReservationID string `msgpack:"reservation_id"`
}

func TestOnActivity_RegistersAndDecodesTypedInputOutput(t *testing.T) {
	withFreshActivityHandlers(t)

	var gotCtx *ActivityContext
	var gotInput reserveInput
	OnActivity("reserve_inventory", func(ctx *ActivityContext, in reserveInput) (reserveOutput, error) {
		gotCtx = ctx
		gotInput = in
		return reserveOutput{ReservationID: "res_1"}, nil
	})

	handler, ok := activityHandlers["reserve_inventory"]
	if !ok {
		t.Fatal("handler not registered under \"reserve_inventory\"")
	}

	payload, err := marshal(reserveInput{OrderID: "ord_1", Qty: 3})
	if err != nil {
		t.Fatalf("marshal fixture: %v", err)
	}

	ctx := &ActivityContext{TenantID: "tenant_1"}
	out, err := handler(ctx, payload)
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	var decoded reserveOutput
	if err := unmarshal(out, &decoded); err != nil {
		t.Fatalf("unmarshal output: %v", err)
	}
	if decoded.ReservationID != "res_1" {
		t.Fatalf("ReservationID = %q, want res_1", decoded.ReservationID)
	}
	if gotCtx != ctx {
		t.Fatal("handler did not receive the same ActivityContext passed in")
	}
	if gotInput != (reserveInput{OrderID: "ord_1", Qty: 3}) {
		t.Fatalf("gotInput = %+v, want {OrderID:ord_1 Qty:3}", gotInput)
	}
}

func TestOnActivity_MalformedPayloadReturnsError(t *testing.T) {
	withFreshActivityHandlers(t)

	OnActivity("reserve_inventory", func(ctx *ActivityContext, in reserveInput) (reserveOutput, error) {
		return reserveOutput{}, nil
	})

	handler := activityHandlers["reserve_inventory"]
	if _, err := handler(&ActivityContext{}, []byte("not valid msgpack")); err == nil {
		t.Fatal("want error for malformed payload, got nil")
	}
}

func TestOnActivity_HandlerErrorPropagates(t *testing.T) {
	withFreshActivityHandlers(t)

	sentinel := errors.New("db unavailable")
	OnActivity("reserve_inventory", func(ctx *ActivityContext, in reserveInput) (reserveOutput, error) {
		return reserveOutput{}, sentinel
	})

	handler := activityHandlers["reserve_inventory"]
	payload, _ := marshal(reserveInput{})
	_, err := handler(&ActivityContext{}, payload)
	if !errors.Is(err, sentinel) {
		t.Fatalf("err = %v, want to wrap %v", err, sentinel)
	}
}

func TestWorkflowApplicationError_DistinguishableViaErrorsAsType(t *testing.T) {
	err := WorkflowApplicationError("inventory.insufficient_stock", map[string]any{"order_id": "ord_1"})

	got, ok := errors.AsType[*NonRetryableActivityError](err)
	if !ok {
		t.Fatal("errors.AsType failed to find *NonRetryableActivityError")
	}
	if got.Type != "inventory.insufficient_stock" {
		t.Fatalf("Type = %q, want inventory.insufficient_stock", got.Type)
	}
	if got.Details["order_id"] != "ord_1" {
		t.Fatalf("Details[order_id] = %v, want ord_1", got.Details["order_id"])
	}
}

func TestWorkflowApplicationError_ErrorStringIsType(t *testing.T) {
	err := WorkflowApplicationError("inventory.insufficient_stock", nil)
	if err.Error() != "inventory.insufficient_stock" {
		t.Fatalf("Error() = %q, want inventory.insufficient_stock", err.Error())
	}
}

func TestPlainError_IsNotNonRetryable(t *testing.T) {
	err := errors.New("transient")
	if _, ok := errors.AsType[*NonRetryableActivityError](err); ok {
		t.Error("plain error matched *NonRetryableActivityError, want no match")
	}
}
