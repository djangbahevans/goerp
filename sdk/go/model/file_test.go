package model

import (
	"testing"

	"github.com/vmihailenco/msgpack/v5"
)

func TestFileDeclarationRoundTrip(t *testing.T) {
	original := Define("contract").Field("signed_pdf_id", File().Label("Signed Document").Required())
	data, err := msgpack.Marshal(original)
	if err != nil {
		t.Fatal(err)
	}

	var got ModelDeclaration
	if err := msgpack.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}

	field := got.Fields[0]
	if field.Name != "signed_pdf_id" || field.Def.Kind.String() != "file" || field.Def.RelatedModel != "" || field.Def.RelationLabel != "Signed Document" || !field.Def.IsRequired {
		t.Fatalf("file declaration: %+v", field)
	}
}
