package model

import (
	"testing"

	"github.com/vmihailenco/msgpack/v5"
)

func TestEnumType_MsgpackRoundTrip(t *testing.T) {
	original := EnumType("order_state_enum", "draft", "confirmed", "done", "cancelled")

	data, err := msgpack.Marshal(original)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var decoded TypeDeclaration
	if err := msgpack.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	if decoded.Name != original.Name {
		t.Errorf("Name = %q, want %q", decoded.Name, original.Name)
	}
	if len(decoded.Values) != len(original.Values) {
		t.Fatalf("got %d values, want %d", len(decoded.Values), len(original.Values))
	}
	for i, v := range original.Values {
		if decoded.Values[i] != v {
			t.Errorf("value %d = %q, want %q", i, decoded.Values[i], v)
		}
	}
}

func TestSchema_Types_MsgpackRoundTrip(t *testing.T) {
	schema := Schema{
		Types: []TypeDeclaration{
			EnumType("order_state_enum", "draft", "confirmed", "done", "cancelled"),
		},
		Models: []*ModelDeclaration{
			Define("sales.order").WithStandardFields(),
		},
	}

	data, err := msgpack.Marshal(schema)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var decoded Schema
	if err := msgpack.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	if len(decoded.Types) != 1 {
		t.Fatalf("got %d types, want 1", len(decoded.Types))
	}
	if decoded.Types[0].Name != "order_state_enum" {
		t.Errorf("Types[0].Name = %q, want %q", decoded.Types[0].Name, "order_state_enum")
	}
	if len(decoded.Models) != 1 {
		t.Fatalf("got %d models, want 1", len(decoded.Models))
	}
}
