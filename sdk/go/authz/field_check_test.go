package authz_test

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/djangbahevans/goerp/sdk/go/authz"
	"github.com/djangbahevans/goerp/sdk/go/orm"
)

type contact struct{}

func (contact) ResourceName() string { return "contacts.contact" }

func TestFieldCheckRejectsEmptyDescriptors(t *testing.T) {
	for _, field := range []orm.FieldDescriptor{
		nil,
		orm.Field[contact, bool]{},
		orm.StringField[contact]{},
		orm.OrderedField[contact, int32]{},
		orm.TimeField[contact]{},
		orm.BytesField[contact]{},
	} {
		if allowed, err := authz.FieldCheck(field, authz.Read); err == nil || allowed {
			t.Errorf("FieldCheck(%T) = %v, %v, want false with an error", field, allowed, err)
		}
	}
}

func TestFieldCheckArgumentTypes(t *testing.T) {
	for _, tc := range []struct {
		name      string
		call      string
		wantError string
	}{
		{"typed fields", `
authz.FieldCheck(orm.NewField[contact, bool]("active"), authz.Read)
authz.FieldCheck(orm.NewStringField[contact]("name"), authz.Write)
authz.FieldCheck(orm.NewOrderedField[contact, int32]("credit_limit"), authz.Read)
authz.FieldCheck(orm.NewTimeField[contact]("created_at"), authz.Write)
authz.FieldCheck(orm.NewBytesField[contact]("metadata"), authz.Read)
authz.FieldCheck(models.KindMatrixFields.IntegerField, authz.Read)
authz.FieldCheck(models.KindMatrixFields.BooleanField, authz.Write)
authz.FieldCheck(models.KindMatrixFields.TimestamptzField, authz.Read)
authz.FieldCheck(models.KindMatrixFields.JsonbField, authz.Write)
authz.FieldCheck(models.KindMatrixFields.CharField, authz.Read)
authz.FieldCheck(models.KindMatrixFields.SelectionField, authz.Write)
authz.FieldCheck(models.KindProbeFields.IntegerField, authz.Read)
`, ""},
		{"string field", `authz.FieldCheck("credit_limit", authz.Read)`, "cannot use"},
		{"string model and field", `authz.FieldCheck("contacts.contact", "credit_limit", authz.Read)`, "too many arguments"},
		{"separate model", `authz.FieldCheck("contacts.address", orm.NewField[contact, bool]("active"), authz.Read)`, "too many arguments"},
		{"field definition", `authz.FieldCheck(model.Integer(), authz.Read)`, "cannot use"},
		{"model definition", `authz.FieldCheck(model.Define("contact"), authz.Read)`, "cannot use"},
		{"unrelated descriptor", `authz.FieldCheck(unrelated{}, authz.Read)`, "missing method isFieldDescriptor"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			source := fmt.Sprintf(`package main
import (
 "github.com/djangbahevans/goerp/sdk/go/authz"
 "github.com/djangbahevans/goerp/sdk/go/model"
 models "github.com/djangbahevans/goerp/sdk/go/modeltest/testdata/fixture/models"
 "github.com/djangbahevans/goerp/sdk/go/orm"
)
type contact struct{}
func (contact) ResourceName() string { return "contacts.contact" }
type unrelated struct{}
func (unrelated) Name() string { return "credit_limit" }
func (unrelated) ResourceName() string { return "contacts.contact" }
func checks() { %s }
func main() {
 _ = model.Integer
 _ = orm.NewStringField[contact]
 _ = models.KindMatrixFields
}
`, tc.call)
			file := filepath.Join(dir, "main.go")
			if err := os.WriteFile(file, []byte(source), 0o600); err != nil {
				t.Fatal(err)
			}

			cmd := exec.CommandContext(t.Context(), "go", "build", "-o", filepath.Join(dir, "fieldcheck"), file)
			output, err := cmd.CombinedOutput()
			if tc.wantError == "" {
				if err != nil {
					t.Fatalf("typed fields failed to compile: %v\n%s", err, output)
				}
			} else if err == nil || !strings.Contains(string(output), tc.wantError) {
				t.Fatalf("build = %v\n%s\nwant %q", err, output, tc.wantError)
			}
		})
	}
}
