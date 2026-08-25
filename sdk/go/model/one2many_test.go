package model

import (
	"testing"

	"github.com/vmihailenco/msgpack/v5"
)

func TestOne2Many_SetsKindRelatedModelAndInverseField(t *testing.T) {
	f := One2Many("contacts.address", "contact_id")
	if f.Kind != KindOne2Many {
		t.Errorf("Kind = %v, want KindOne2Many", f.Kind)
	}
	if f.RelatedModel != "contacts.address" {
		t.Errorf("RelatedModel = %q, want contacts.address", f.RelatedModel)
	}
	if f.InverseField != "contact_id" {
		t.Errorf("InverseField = %q, want contact_id", f.InverseField)
	}
}

func TestOne2Many_MsgpackRoundTrip(t *testing.T) {
	original := Define("contacts.contact").
		Field("id", UUID().Required().PrimaryKey()).
		Field("address_ids", One2Many("contacts.address", "contact_id"))

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
		if f.Name == "address_ids" {
			found = true
			if f.Def.Kind != KindOne2Many {
				t.Errorf("Kind = %v, want KindOne2Many", f.Def.Kind)
			}
			if f.Def.RelatedModel != "contacts.address" {
				t.Errorf("RelatedModel = %q, want contacts.address", f.Def.RelatedModel)
			}
			if f.Def.InverseField != "contact_id" {
				t.Errorf("InverseField = %q, want contact_id", f.Def.InverseField)
			}
		}
	}
	if !found {
		t.Fatal("address_ids field not found after round trip")
	}
}
