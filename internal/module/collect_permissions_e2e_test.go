package module

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const permissionsFixtureManifest = "{\n  \"name\": \"widgets\",\n  \"version\": \"1.0.0\",\n  \"views\": [\n    {\"name\": \"widget_list\", \"permission\": \"widgets:widget:read\"}\n  ]\n}\n"

const permissionsFixtureMain = `package main

import "github.com/djangbahevans/goerp/sdk/go/perm"

var (
	widgetRead = perm.Define("widgets:widget:read", perm.Description("View widgets"), perm.DefaultRoles(perm.User, perm.Admin))
	contactRead = perm.Ref("contacts:contact:read")
	_ = perm.DefinePolicy("widgets:widget:own_only", widgetRead, "record.owner_id = current_user.id", perm.Description("Own widgets"))
	_ = perm.DefinePolicy("widgets:widget:contact_scoped", contactRead, "true")
)

func main() {}
`

func TestGenerate_PermissionsProduceTheirManifestBlocks(t *testing.T) {
	dir := writeCollectFixture(t, permissionsFixtureMain, permissionsFixtureManifest)
	ctx := generateCtx(t)

	result, err := Generate(ctx, dir, GenerateOptions{})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if got := strings.Join(result.Blocks, ","); got != "permissions,policies,uses_permissions" {
		t.Errorf("Blocks = %s, want permissions,policies,uses_permissions", got)
	}

	got := readFile(t, filepath.Join(dir, "manifest.json"))
	for _, want := range []string{
		`"name": "widgets:widget:read"`, `"description": "View widgets"`, `"default_roles": [`,
		`"applies_to": "widgets:widget:read"`, `"applies_to": "contacts:contact:read"`,
		`"uses_permissions": [`, `"contacts:contact:read"`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("manifest.json lacks %s:\n%s", want, got)
		}
	}

	if _, err := Generate(ctx, dir, GenerateOptions{Check: true}); err != nil {
		t.Errorf("--check on fresh output: %v", err)
	}

	changed := strings.Replace(permissionsFixtureMain, "View widgets", "See widgets", 1)
	if err := os.WriteFile(filepath.Join(dir, "cmd", "module", "main.go"), []byte(changed), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Generate(ctx, dir, GenerateOptions{Check: true}); err == nil || !strings.Contains(err.Error(), "permissions") {
		t.Fatalf("--check after changing a description = %v, want it to name permissions", err)
	}
}

func TestGenerate_PermissionFailuresFailGeneration(t *testing.T) {
	tests := []struct {
		name     string
		main     []string
		manifest string
		want     string
	}{
		{
			name:     "view names an undeclared permission",
			manifest: strings.Replace(permissionsFixtureManifest, "widgets:widget:read", "widgets:widget:typo", 1),
			want:     `view "widget_list": permission names permission "widgets:widget:typo"`,
		},
		{
			name: "define in another module's namespace",
			main: []string{`"widgets:widget:read"`, `"contacts:contact:read"`},
			want: `perm.Define module segment "contacts"`,
		},
		{
			name: "ref to the module's own permission",
			main: []string{`perm.Ref("contacts:contact:read")`, `perm.Ref("widgets:widget:other")`},
			want: `perm.Ref names the module's own permission`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			manifest := permissionsFixtureManifest
			if tt.manifest != "" {
				manifest = tt.manifest
			}
			src := permissionsFixtureMain
			if tt.main != nil {
				src = strings.NewReplacer(tt.main...).Replace(src)
			}
			dir := writeCollectFixture(t, src, manifest)

			_, err := Generate(generateCtx(t), dir, GenerateOptions{})
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("Generate = %v, want it to contain %q", err, tt.want)
			}
			if got := readFile(t, filepath.Join(dir, "manifest.json")); got != manifest {
				t.Errorf("manifest.json was written:\n%s", got)
			}
		})
	}
}
