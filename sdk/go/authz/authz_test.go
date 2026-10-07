package authz

import (
	"errors"
	"testing"

	abi "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/djangbahevans/goerp/sdk/go/perm"
	"github.com/vmihailenco/msgpack/v5"
)

func TestFieldCheckInput_MsgpackRoundTrip(t *testing.T) {
	in := abi.AuthzFieldCheckInput{
		Model: "contacts.contact",
		Field: "credit_limit",
		Kind:  Write,
	}

	raw, err := msgpack.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var got abi.AuthzFieldCheckInput
	if err := msgpack.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got != in {
		t.Fatalf("got %+v, want %+v", got, in)
	}
}

func TestFieldCheckOutput_MsgpackRoundTrip(t *testing.T) {
	out := abi.AuthzFieldCheckOutput{Allowed: true}

	raw, err := msgpack.Marshal(out)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var got abi.AuthzFieldCheckOutput
	if err := msgpack.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got != out {
		t.Fatalf("got %+v, want %+v", got, out)
	}
}

func TestCheckInput_MsgpackRoundTrip(t *testing.T) {
	in := abi.AuthzCheckInput{Permission: "sales:order:confirm", ResourceID: "order_1"}

	raw, err := msgpack.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got abi.AuthzCheckInput
	if err := msgpack.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got != in {
		t.Fatalf("got %+v, want %+v", got, in)
	}
}

func TestForbiddenOrErr(t *testing.T) {
	p := perm.Ref("sales:order:confirm")
	forbidden := &abi.HostError{Code: abi.ErrCodeAuthzForbidden, Message: "caller does not hold permission"}
	other := &abi.HostError{Code: abi.ErrCodeCapabilityDenied, Message: "no capability"}

	if err := forbiddenOrErr(p, nil); err != nil {
		t.Errorf("nil error = %v, want nil", err)
	}

	err := forbiddenOrErr(p, forbidden)
	fe, ok := errors.AsType[*ForbiddenError](err)
	if !ok || fe.Permission != "sales:order:confirm" {
		t.Fatalf("forbidden error = %v, want a *ForbiddenError for the permission", err)
	}
	if he, ok := errors.AsType[*abi.HostError](err); !ok || he != forbidden {
		t.Errorf("ForbiddenError does not unwrap to the host error: %v", err)
	}

	if err := forbiddenOrErr(p, other); err != error(other) {
		t.Errorf("other host error = %v, want it returned unchanged", err)
	}
}

func TestCheckAndRequire_RejectTheZeroPermission(t *testing.T) {
	if _, err := Check(perm.Permission{}, ""); !errors.Is(err, errZeroPermission) {
		t.Errorf("Check = %v, want errZeroPermission", err)
	}
	if err := Require(perm.Permission{}, ""); !errors.Is(err, errZeroPermission) {
		t.Errorf("Require = %v, want errZeroPermission", err)
	}
	if _, err := RowFilter("invoice", perm.Permission{}); !errors.Is(err, errZeroPermission) {
		t.Errorf("RowFilter = %v, want errZeroPermission", err)
	}
}
