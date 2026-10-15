package loader

import (
	"strings"
	"testing"

	"github.com/djangbahevans/goerp/internal/engine/module"
	"github.com/djangbahevans/goerp/sdk/go/model"
)

func TestFileRejectsExtensionReadKeyCollision(t *testing.T) {
	modules := modelExtensionModules()
	modules["contacts"].ModelDecls[0].Fields = append(modules["contacts"].ModelDecls[0].Fields, model.NamedField{Name: "avatar_id", Def: model.File()})
	modules["industry"].ModelExtensions = []model.ModelExtension{model.Extend("contacts.contact").Field("avatar", model.Text())}

	validateModelReferences(modules)
	owner := modules["contacts"]
	if owner.Status != module.StatusFailed || !strings.Contains(owner.FailureReason, "read key") {
		t.Fatalf("conflicting file read key accepted: status=%v reason=%s", owner.Status, owner.FailureReason)
	}
}

func TestFileMultiExtensionValidation(t *testing.T) {
	modules := modelExtensionModules()
	modules["industry"].ModelExtensions = []model.ModelExtension{model.Extend("contacts.contact").Field("attachment_ids", model.FileMulti("contact_attachments"))}
	validateModelReferences(modules)
	for name, m := range modules {
		if m.Status == module.StatusFailed {
			t.Fatalf("%s: %s", name, m.FailureReason)
		}
	}
	modules["industry"].ModelExtensions[0].Fields[0].Def.JoinTable = "files"
	validateModelReferences(modules)
	if m := modules["industry"]; m.Status != module.StatusFailed {
		t.Fatal("reserved join table accepted")
	}
}
