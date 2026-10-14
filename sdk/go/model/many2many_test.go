package model

import (
	"testing"

	"github.com/vmihailenco/msgpack/v5"
)

func TestMany2ManyMetadataRoundTrip(t *testing.T) {
	field := Many2Many("contacts.tag", Through("contact_tag_assignments"), M2MColumn1("contact_id"), M2MColumn2("tag_id")).Label("Tags")
	data, err := msgpack.Marshal(field)
	if err != nil {
		t.Fatal(err)
	}
	var decoded FieldDef
	if err := msgpack.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Kind.String() != "many2many" || decoded.RelatedModel != "contacts.tag" || decoded.JoinTable != "contact_tag_assignments" || decoded.Column1 != "contact_id" || decoded.Column2 != "tag_id" || decoded.RelationLabel != "Tags" {
		t.Fatalf("lost declaration metadata: %+v", decoded)
	}
}
