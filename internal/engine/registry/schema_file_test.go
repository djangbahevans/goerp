package registry

import (
	"encoding/json/v2"
	"testing"

	"github.com/djangbahevans/goerp/sdk/go/model"
)

func TestSchemaFileReadKey(t *testing.T) {
	md := model.Define("contracts.contract").Field("signed_pdf_id", model.File().Label("Signed Document").Required())
	field := schemaModelFrom(*md, nil).Fields[0]
	data, err := json.Marshal(field)
	if err != nil {
		t.Fatal(err)
	}

	var wire map[string]any
	if err := json.Unmarshal(data, &wire); err != nil {
		t.Fatal(err)
	}
	if wire["type"] != "file" || wire["read_key"] != "signed_pdf" || field.RelatedModel != "" || !field.Required {
		t.Fatalf("schema: %s", data)
	}
}

func TestSchemaFileMultiReadKey(t *testing.T) {
	md := model.Define("contracts.contract").Field("attachment_ids", model.FileMulti("contract_attachments"))
	field := schemaModelFrom(*md, nil).Fields[0]
	if field.Type != "file_multi" || field.ReadKey != "attachments" || field.RelatedModel != "" {
		t.Fatalf("schema: %+v", field)
	}
}
