package model

import (
	"testing"

	"github.com/vmihailenco/msgpack/v5"
)

func TestDynamicLink_SetsKindAndReferenceTypeField(t *testing.T) {
	f := DynamicLink("reference_type")
	if f.Kind != KindDynamicLink {
		t.Errorf("Kind = %v, want KindDynamicLink", f.Kind)
	}
	if f.ReferenceTypeField != "reference_type" {
		t.Errorf("ReferenceTypeField = %q, want reference_type", f.ReferenceTypeField)
	}
}

func TestFieldDef_Tree_SetsIsTree(t *testing.T) {
	f := Many2One("contacts.category").Tree()
	if !f.IsTree {
		t.Fatal("expected IsTree to be true after .Tree()")
	}
	if f.Kind != KindMany2One {
		t.Errorf("Kind = %v, want KindMany2One (Tree is a modifier, not a separate kind)", f.Kind)
	}
}

func TestFieldDef_Tree_DefaultsFalse(t *testing.T) {
	f := Many2One("contacts.category")
	if f.IsTree {
		t.Fatal("expected IsTree to default to false")
	}
}

func TestDynamicLink_MsgpackRoundTrip(t *testing.T) {
	original := Define("comments.comment").
		Field("id", UUID().Required().PrimaryKey()).
		Field("reference_type", Selection("sales.order", "contacts.contact")).
		Field("reference_id", DynamicLink("reference_type"))

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
		if f.Name == "reference_id" {
			found = true
			if f.Def.Kind != KindDynamicLink {
				t.Errorf("Kind = %v, want KindDynamicLink", f.Def.Kind)
			}
			if f.Def.ReferenceTypeField != "reference_type" {
				t.Errorf("ReferenceTypeField = %q, want reference_type", f.Def.ReferenceTypeField)
			}
		}
	}
	if !found {
		t.Fatal("reference_id field not found after round trip")
	}
}

func TestTree_MsgpackRoundTrip(t *testing.T) {
	original := Define("contacts.category").
		Field("id", UUID().Required().PrimaryKey()).
		Field("parent_id", Many2One("contacts.category").Tree())

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
		if f.Name == "parent_id" {
			found = true
			if !f.Def.IsTree {
				t.Error("expected decoded parent_id field to have IsTree = true")
			}
			if f.Def.RelatedModel != "contacts.category" {
				t.Errorf("RelatedModel = %q, want contacts.category", f.Def.RelatedModel)
			}
		}
	}
	if !found {
		t.Fatal("parent_id field not found after round trip")
	}
}
