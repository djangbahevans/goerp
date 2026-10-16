package model

import (
	"testing"

	"github.com/vmihailenco/msgpack/v5"
)

func TestDelegateDeclarationRoundTrip(t *testing.T) {
	field := Delegate("contacts.contact").Required()
	if field.Kind.String() != "delegate" || field.RelatedModel != "contacts.contact" || field.RelationOnDelete != Restrict {
		t.Fatalf("invalid Delegate declaration: %+v", field)
	}
	data, err := msgpack.Marshal(field)
	if err != nil {
		t.Fatal(err)
	}
	var decoded FieldDef
	if err := msgpack.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Kind != KindDelegate || !decoded.IsRequired || decoded.RelatedModel != "contacts.contact" {
		t.Fatalf("Delegate metadata lost across WASM: %+v", decoded)
	}
}
