package model

import (
	"testing"

	"github.com/vmihailenco/msgpack/v5"
)

func TestFileDeclarationRoundTrip(t *testing.T) {
	original := Define("contract").Field("signed_pdf_id", File().Label("Signed Document").Required()).
		Field("attachment_ids", FileMulti("contract_attachments").Label("Attachments").Required())
	data, err := msgpack.Marshal(original)
	if err != nil {
		t.Fatal(err)
	}

	var got ModelDeclaration
	if err := msgpack.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}

	multi := got.Fields[1]
	if multi.Def.Kind.String() != "file_multi" || multi.Def.JoinTable != "contract_attachments" || multi.Def.RelatedModel != "" || !multi.Def.IsRequired {
		t.Fatalf("multi-file declaration: %+v", multi)
	}
	field := got.Fields[0]
	if field.Name != "signed_pdf_id" || field.Def.Kind.String() != "file" || field.Def.RelatedModel != "" || field.Def.RelationLabel != "Signed Document" || !field.Def.IsRequired {
		t.Fatalf("file declaration: %+v", field)
	}
}
