package authz

import (
	"testing"

	"github.com/vmihailenco/msgpack/v5"
)

func TestFieldCheckInput_MsgpackRoundTrip(t *testing.T) {
	in := fieldCheckInput{
		UserID: "user_1",
		Model:  "contacts.contact",
		Field:  "credit_limit",
		Kind:   Write,
	}

	raw, err := msgpack.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var got fieldCheckInput
	if err := msgpack.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got != in {
		t.Fatalf("got %+v, want %+v", got, in)
	}
}

func TestFieldCheckOutput_MsgpackRoundTrip(t *testing.T) {
	out := fieldCheckOutput{Allowed: true}

	raw, err := msgpack.Marshal(out)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var got fieldCheckOutput
	if err := msgpack.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got != out {
		t.Fatalf("got %+v, want %+v", got, out)
	}
}
