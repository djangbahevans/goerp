package schema

import (
	"strings"
	"testing"

	"github.com/djangbahevans/goerp/sdk/go/model"
)

func TestToAtlasSchema_DynamicLinkRejectsConflictingReadKeys(t *testing.T) {
	for _, name := range []string{"reference", "reference_id"} {
		t.Run(name, func(t *testing.T) {
			md := model.Define("widgets.comment").
				Field("id", model.UUID().PrimaryKey()).
				Field("reference_type", model.Selection("widgets.widget")).
				Field(name, model.DynamicLink("reference_type"))
			if name == "reference_id" {
				md.Field("reference", model.Text())
			}

			_, err := ToAtlasSchema("tenant_test", "widgets", []model.ModelDeclaration{*md}, nil)
			if err == nil || !strings.Contains(err.Error(), name) {
				t.Fatalf("schema accepts a conflicting DynamicLink read key: %v", err)
			}
		})
	}
}
