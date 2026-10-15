package module

import (
	"strings"
	"testing"

	"github.com/djangbahevans/goerp/sdk/go/model"
)

func TestRenderModelFile_DynamicLinkRejectsConflictingReadKeys(t *testing.T) {
	for _, name := range []string{"reference", "reference_id"} {
		t.Run(name, func(t *testing.T) {
			md := model.Define("widgets.attachment").
				Field("reference_type", model.Selection("widgets.widget")).
				Field(name, model.DynamicLink("reference_type"))
			if name == "reference_id" {
				md.Field("reference", model.Text())
			}

			_, _, err := renderModelFile(md, nil, testGenContext())
			if err == nil || !strings.Contains(err.Error(), name) {
				t.Fatalf("generation accepts a conflicting DynamicLink read key: %v", err)
			}
		})
	}
}
