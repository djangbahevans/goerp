package module

import (
	"strings"
	"testing"

	"github.com/djangbahevans/goerp/sdk/go/model"
)

func TestGenerateDelegateFields(t *testing.T) {
	contact := model.Define("hr.contact").WithStandardFields().Field("name", model.Text().Required())
	employee := model.Define("hr.employee").WithStandardFields().Field("contact_id", model.Delegate("hr.contact")).Field("department", model.Text())
	ctx := genContext{moduleName: "hr", modelsByResource: map[string]*model.ModelDeclaration{"contact": contact, "employee": employee}}
	src, _, err := renderModelFile(employee, nil, ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"ContactID *string", "Name *string", "SetName(", "SetContactID(", `row["name"]`} {
		if !strings.Contains(normalizeSpaces(string(src)), want) {
			t.Errorf("generated model missing %s:\n%s", want, src)
		}
	}
	if strings.Contains(string(src), "orm.Ref[") {
		t.Fatal("Delegate must flatten fields instead of producing a Many2One expansion")
	}
}
