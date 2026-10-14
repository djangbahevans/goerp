package module

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

const m2mGenerateFixture = `package schema
import "github.com/djangbahevans/goerp/sdk/go/model"
var Schema = model.Schema{Models: []*model.ModelDeclaration{
 model.Define("widgets.contact").WithStandardFields().Field("name", model.Text()).
  Field("tag_ids", model.Many2Many("widgets.tag", model.Through("assignments"), model.M2MColumn1("contact_id"), model.M2MColumn2("tag_id"))).
  Field("external_ids", model.Many2Many("other.tag", model.Through("external_assignments"), model.M2MColumn1("contact_id"), model.M2MColumn2("tag_id"))),
 model.Define("widgets.tag").WithStandardFields().Field("name", model.Text()).Field("color", model.Text()),
}}
`

func TestGenerateMany2ManyCompilesAndScans(t *testing.T) {
	dir := writeGenerateFixtureWithManifest(t, m2mGenerateFixture, generateFixtureManifest("widgets", "other"))
	if _, err := Generate(t.Context(), dir, GenerateOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := Generate(t.Context(), dir, GenerateOptions{Check: true}); err != nil {
		t.Fatal(err)
	}
	usage := `package models
import (
 "testing"
 "github.com/djangbahevans/goerp/sdk/go/orm"
)
func TestMany2ManyScan(t *testing.T) {
 var row Contact
 if err := row.Scan(map[string]any{
  "tag_ids": []any{"t1"},
  "tags": []any{map[string]any{"id": "t1", "name": "VIP", "color": "#112233"}},
  "external_ids": []any{"e1"},
  "externals": []any{map[string]any{"id": "e1", "name": "External"}},
 }); err != nil { t.Fatal(err) }
 if len(row.TagIDs) != 1 || len(row.Tags) != 1 || row.Tags[0].Name == nil || *row.Tags[0].Name != "VIP" || row.Tags[0].Color == nil || *row.Tags[0].Color != "#112233" { t.Fatalf("scan: %+v", row) }
 if len(row.Externals) != 1 || row.Externals[0].Fields["name"] != "External" { t.Fatalf("external scan: %+v", row.Externals) }
 values := NewContactValues().SetTagIDs([]string{"t1"}).SetExternalIDs([]string{})
 var _ *orm.Values[Contact] = &values.Values
 var _ orm.Field[Contact, []string] = ContactFields.TagIDs
 if err := row.Scan(map[string]any{"tags": []any{"bad"}}); err == nil { t.Fatal("invalid record array accepted") }
 if err := row.Scan(map[string]any{"tag_ids": []any{42}}); err == nil { t.Fatal("invalid ID array accepted") }
}
`
	if err := os.WriteFile(filepath.Join(dir, "models", "usage_test.go"), []byte(usage), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "go", "test", "./models")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOFLAGS=-buildvcs=false")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("generated models: %v\n%s", err, out)
	}
}
