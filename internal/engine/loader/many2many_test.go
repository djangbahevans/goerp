package loader

import (
	"strings"
	"testing"

	"github.com/djangbahevans/goerp/internal/engine/module"
	"github.com/djangbahevans/goerp/sdk/go/model"
)

func TestMany2ManyRejectsExtensionReadKeyCollision(t *testing.T) {
	modules := modelExtensionModules()
	modules["contacts"].ModelDecls[0].Fields = append(modules["contacts"].ModelDecls[0].Fields, model.NamedField{
		Name: "tag_ids",
		Def:  model.Many2Many("contacts.contact", model.Through("contact_tags"), model.M2MColumn1("contact_id"), model.M2MColumn2("tag_id")),
	})
	modules["industry"].ModelExtensions = []model.ModelExtension{
		model.Extend("contacts.contact").Field("tags", model.Text()),
	}

	validateModelReferences(modules)
	owner := modules["contacts"]
	if owner.Status != module.StatusFailed || !strings.Contains(owner.FailureReason, "read key") {
		t.Fatalf("conflicting expanded key was accepted: status=%v, reason=%q", owner.Status, owner.FailureReason)
	}
}
