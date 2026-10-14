package registry

import (
	"testing"

	"github.com/djangbahevans/goerp/sdk/go/model"
)

func TestSchemaMany2ManyReadKey(t *testing.T) {
	md := model.Define("contacts.contact").Field("tag_ids", model.Many2Many("contacts.tag", model.Through("assignments"), model.M2MColumn1("contact_id"), model.M2MColumn2("tag_id")))
	field := schemaModelFrom(*md, nil).Fields[0]
	if field.Type != "many2many" || field.ReadKey != "tags" || field.RelatedModel != "contacts.tag" {
		t.Fatalf("schema: %+v", field)
	}
}
