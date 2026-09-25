package authz

import (
	"testing"

	abi "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/vmihailenco/msgpack/v5"
)

func TestFieldCheckInput_MsgpackRoundTrip(t *testing.T) {
	in := abi.AuthzFieldCheckInput{
		UserID: "user_1",
		Model:  "contacts.contact",
		Field:  "credit_limit",
		Kind:   Write,
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
