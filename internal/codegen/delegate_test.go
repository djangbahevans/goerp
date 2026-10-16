package codegen

import (
	"testing"

	"github.com/djangbahevans/goerp/sdk/go/model"
)

func TestLocalDelegateFields(t *testing.T) {
	in := localInput(&localManifest{Name: "hr"}, nil, model.Schema{Models: []*model.ModelDeclaration{
		model.Define("hr.employee").WithStandardFields().Field("contact_id", model.Delegate("hr.contact").Required()),
		model.Define("hr.contact").WithStandardFields().Field("name", model.Text().Required()),
	}})
	fields := map[string]Field{}
	for _, f := range in.Models[0].Fields {
		fields[f.Name] = f
	}
	if fields["contact_id"].Kind != "delegate" || fields["name"].Kind != "text" || fields["name"].Required {
		t.Fatalf("delegate fields missing or required for links: %+v", fields)
	}
	if ts, ok := fieldTSType(fields["contact_id"]); !ok || ts != "string" {
		t.Fatalf("delegate FK type = %s, %v", ts, ok)
	}
}
