package model

import (
	"testing"

	"github.com/vmihailenco/msgpack/v5"
)

func TestFieldDef_Primary_SetsIsPrimary(t *testing.T) {
	f := Text().Primary()
	if !f.IsPrimary {
		t.Fatal("expected IsPrimary to be true after .Primary()")
	}
}

func TestFieldDef_Primary_DefaultsFalse(t *testing.T) {
	f := Text()
	if f.IsPrimary {
		t.Fatal("expected IsPrimary to default to false")
	}
}

func TestFieldDef_Primary_WorksOnAnyFieldType(t *testing.T) {
	cases := []FieldDef{
		Char().Primary(),
		Text().Primary(),
		Integer().Primary(),
		UUID().Primary(),
	}
	for i, f := range cases {
		if !f.IsPrimary {
			t.Errorf("case %d: expected IsPrimary to be true", i)
		}
	}
}

func TestFieldDef_Primary_MsgpackRoundTrip(t *testing.T) {
	original := Define("sales.contact").
		Field("id", UUID().Required().PrimaryKey()).
		Field("name", Text().Required().Primary())

	data, err := msgpack.Marshal(original)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var decoded ModelDeclaration
	if err := msgpack.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	var found bool
	for _, f := range decoded.Fields {
		if f.Name == "name" {
			found = true
			if !f.Def.IsPrimary {
				t.Error("expected decoded name field to have IsPrimary = true")
			}
		}
		if f.Name == "id" && f.Def.IsPrimary {
			t.Error("expected decoded id field to have IsPrimary = false")
		}
	}
	if !found {
		t.Fatal("name field not found after round trip")
	}
}
