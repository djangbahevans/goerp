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
