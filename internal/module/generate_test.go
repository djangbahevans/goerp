package module

import (
	"context"
	"fmt"
	"go/format"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const generateFixtureSchemaOneModel = `package schema

import "github.com/djangbahevans/goerp/sdk/go/model"

var Schema = model.Schema{
	Models: []*model.ModelDeclaration{
		model.Define("widgets.widget", model.Table("widgets")).
			WithStandardFields().
			Field("name", model.Text().Required()),
	},
}
`

const generateFixtureSchemaTwoModels = `package schema

import "github.com/djangbahevans/goerp/sdk/go/model"

var Schema = model.Schema{
	Models: []*model.ModelDeclaration{
		model.Define("widgets.widget", model.Table("widgets")).
			WithStandardFields().
			Field("name", model.Text().Required()),
		model.Define("widgets.gadget", model.Table("gadgets")).
			WithStandardFields().
			Field("name", model.Text().Required()),
	},
}
`

const generateFixtureSchemaResourceNameCollision = `package schema

import "github.com/djangbahevans/goerp/sdk/go/model"

var Schema = model.Schema{
	Models: []*model.ModelDeclaration{
		model.Define("sales.order", model.Table("sales_orders")).
			WithStandardFields(),
		model.Define("purchase.order", model.Table("purchase_orders")).
			WithStandardFields(),
	},
}
`

// gadget_fields generates GadgetFields, colliding with the descriptor variable generated
// for gadget despite distinct filenames.
const generateFixtureSchemaPackageIdentifierCollision = `package schema

import "github.com/djangbahevans/goerp/sdk/go/model"

var Schema = model.Schema{
	Models: []*model.ModelDeclaration{
		model.Define("widgets.gadget", model.Table("gadgets")).
			WithStandardFields(),
		model.Define("widgets.gadget_fields", model.Table("gadget_fields")).
			WithStandardFields(),
	},
}
`

const generateFixtureSchemaCrossModuleMany2One = `package schema

import "github.com/djangbahevans/goerp/sdk/go/model"

var Schema = model.Schema{
	Models: []*model.ModelDeclaration{
		model.Define("widgets.gadget", model.Table("gadgets")).
			WithStandardFields().
			Field("owner_id", model.Many2One("contacts.contact").Required()),
	},
}
`

const generateFixtureSchemaEmpty = `package schema

import "github.com/djangbahevans/goerp/sdk/go/model"

var Schema = model.Schema{}
`

const generateFixtureSchemaCompileError = `package schema

import "github.com/djangbahevans/goerp/sdk/go/model"

var Schema = model.Schema{
	Models: []*model.ModelDeclaration{
		this does not compile,
	},
}
`

// generateFixtureManifest is the minimal manifest.json loadGenContext
// needs — a bare "name"/"depends_on"/"soft_depends_on", not a
// manifest.Load-valid Manifest — for a fixture module named moduleName,
// depending on every module in dependsOn.
func generateFixtureManifest(moduleName string, dependsOn ...string) string {
	deps := `"` + strings.Join(dependsOn, `","`) + `"`
	if len(dependsOn) == 0 {
		deps = ""
	}
	return fmt.Sprintf(`{"name": %q, "depends_on": [%s]}`, moduleName, deps)
}

func generateFixtureSchemaImportingModels(modulePath string) string {
	return `package schema

import (
	"github.com/djangbahevans/goerp/sdk/go/model"

	_ "` + modulePath + `/models"
)

var Schema = model.Schema{}
`
}

// writeEmptyCmdModule gives dir the cmd/module package that collecting
// declarations builds.
func writeEmptyCmdModule(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, "cmd", "module"), 0o755); err != nil {
		t.Fatalf("mkdir cmd/module: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "cmd", "module", "main.go"), []byte("package main\n\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatalf("write cmd/module/main.go: %v", err)
	}
}

// writeGenerateFixture scaffolds a standalone module at t.TempDir()/demo
// with the given schema/schema.go content, then overlays a go.work
// workspace onto this repo's own checkout so the fixture's schema
// package — which imports sdk/go/model — resolves without needing a
// require in the fixture's own go.mod (Generate itself never touches
// go.mod; this is purely a test-resolution concern, the same one
// scaffold_test.go's TestCreateScaffoldsCompilableSchemaPackage solves
// the same way).
func writeGenerateFixture(t *testing.T, schemaGo string) string {
	t.Helper()
	return writeGenerateFixtureWithManifest(t, schemaGo, generateFixtureManifest("widgets"))
}

func writeGenerateFixtureWithManifest(t *testing.T, schemaGo, manifestJSON string) string {
	t.Helper()

	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("resolve repo root: %v", err)
	}

	dir := filepath.Join(t.TempDir(), "demo_module")
	if err := os.MkdirAll(filepath.Join(dir, "schema"), 0o755); err != nil {
		t.Fatalf("mkdir schema: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module generate-fixture\n\ngo 1.27.0\n"), 0o644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "schema", "schema.go"), []byte(schemaGo), 0o644); err != nil {
		t.Fatalf("write schema.go: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), []byte(manifestJSON), 0o644); err != nil {
		t.Fatalf("write manifest.json: %v", err)
	}
	writeEmptyCmdModule(t, dir)

	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()

	workInit := exec.CommandContext(ctx, "go", "work", "init", ".", repoRoot)
	workInit.Dir = dir
	if out, err := workInit.CombinedOutput(); err != nil {
		t.Fatalf("go work init: %v\n%s", err, out)
	}

	return dir
}

// Nested module directories require import paths relative to their containing Go module
// root.
func TestGenerate_DirIsSubdirectoryOfLargerModule(t *testing.T) {
	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("resolve repo root: %v", err)
	}

	moduleRoot := t.TempDir()
	targetDir := filepath.Join(moduleRoot, "services", "widgets")
	if err := os.MkdirAll(filepath.Join(targetDir, "schema"), 0o755); err != nil {
		t.Fatalf("mkdir schema: %v", err)
	}
	if err := os.WriteFile(filepath.Join(moduleRoot, "go.mod"), []byte("module generate-subdir-fixture\n\ngo 1.27.0\n"), 0o644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	if err := os.WriteFile(filepath.Join(targetDir, "schema", "schema.go"), []byte(generateFixtureSchemaOneModel), 0o644); err != nil {
		t.Fatalf("write schema.go: %v", err)
	}
	if err := os.WriteFile(filepath.Join(targetDir, "manifest.json"), []byte(generateFixtureManifest("widgets")), 0o644); err != nil {
		t.Fatalf("write manifest.json: %v", err)
	}
	writeEmptyCmdModule(t, targetDir)

	workCtx, workCancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer workCancel()
	workInit := exec.CommandContext(workCtx, "go", "work", "init", ".", repoRoot)
	workInit.Dir = moduleRoot
	if out, err := workInit.CombinedOutput(); err != nil {
		t.Fatalf("go work init: %v\n%s", err, out)
	}

	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()

	result, err := Generate(ctx, targetDir, GenerateOptions{})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if len(result.Stale) != 1 || result.Stale[0] != filepath.Join("models", "widget.gen.go") {
		t.Fatalf("Stale = %v, want [models/widget.gen.go]", result.Stale)
	}
	if _, err := os.Stat(filepath.Join(targetDir, "models", "widget.gen.go")); err != nil {
		t.Errorf("expected models/widget.gen.go under targetDir: %v", err)
	}
}

// Choose the innermost containing Go module when workspace roots overlap.
func TestModuleImportPath_NestedMainModulesPicksMostSpecific(t *testing.T) {
	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("resolve repo root: %v", err)
	}

	outerRoot := t.TempDir()
	innerRoot := filepath.Join(outerRoot, "vendor", "inner")
	targetDir := filepath.Join(innerRoot, "services", "widgets")
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		t.Fatalf("mkdir targetDir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(outerRoot, "go.mod"), []byte("module generate-nested-outer\n\ngo 1.27.0\n"), 0o644); err != nil {
		t.Fatalf("write outer go.mod: %v", err)
	}
	if err := os.WriteFile(filepath.Join(innerRoot, "go.mod"), []byte("module generate-nested-inner\n\ngo 1.27.0\n"), 0o644); err != nil {
		t.Fatalf("write inner go.mod: %v", err)
	}

	workCtx, workCancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer workCancel()
	workInit := exec.CommandContext(workCtx, "go", "work", "init", outerRoot, innerRoot, repoRoot)
	workInit.Dir = outerRoot
	if out, err := workInit.CombinedOutput(); err != nil {
		t.Fatalf("go work init: %v\n%s", err, out)
	}

	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()

	got, err := moduleImportPath(ctx, targetDir)
	if err != nil {
		t.Fatalf("moduleImportPath: %v", err)
	}
	want := "generate-nested-inner/services/widgets"
	if got != want {
		t.Errorf("moduleImportPath = %q, want %q (the innermost containing module, not the outer one)", got, want)
	}
}

// A replace-based fixture exercises the single-module path; both directory operands must
// resolve symlinks before deriving import paths.
func TestGenerate_DirReachedThroughSymlink(t *testing.T) {
	dir := writeGenerateFixtureSingleModule(t, generateFixtureSchemaOneModel)

	symlinkParent := t.TempDir()
	symlinkPath := filepath.Join(symlinkParent, "via-symlink")
	if err := os.Symlink(dir, symlinkPath); err != nil {
		t.Skipf("symlinks not supported here: %v", err)
	}

	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()

	result, err := Generate(ctx, symlinkPath, GenerateOptions{})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if len(result.Stale) != 1 || result.Stale[0] != filepath.Join("models", "widget.gen.go") {
		t.Fatalf("Stale = %v, want [models/widget.gen.go]", result.Stale)
	}
	if _, err := os.Stat(filepath.Join(dir, "models", "widget.gen.go")); err != nil {
		t.Errorf("expected models/widget.gen.go under the real (non-symlinked) dir: %v", err)
	}
}

// writeGenerateFixtureSingleModule is writeGenerateFixture's single-
// main-module counterpart: a `replace` directive resolves the SDK
// dependency against this repo's own checkout instead of a go.work
// workspace, so `go list -m` reports exactly one main module — the fast
// path TestGenerate_DirReachedThroughSymlink needs to actually exercise.
func writeGenerateFixtureSingleModule(t *testing.T, schemaGo string) string {
	t.Helper()

	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("resolve repo root: %v", err)
	}

	dir := filepath.Join(t.TempDir(), "demo_module")
	if err := os.MkdirAll(filepath.Join(dir, "schema"), 0o755); err != nil {
		t.Fatalf("mkdir schema: %v", err)
	}
	goMod := fmt.Sprintf("module generate-fixture-single\n\ngo 1.27.0\n\nrequire github.com/djangbahevans/goerp v0.0.0\n\nreplace github.com/djangbahevans/goerp => %s\n", repoRoot)
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(goMod), 0o644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "schema", "schema.go"), []byte(schemaGo), 0o644); err != nil {
		t.Fatalf("write schema.go: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), []byte(generateFixtureManifest("widgets")), 0o644); err != nil {
		t.Fatalf("write manifest.json: %v", err)
	}
	writeEmptyCmdModule(t, dir)

	ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
	defer cancel()

	tidy := exec.CommandContext(ctx, "go", "mod", "tidy")
	tidy.Dir = dir
	if out, err := tidy.CombinedOutput(); err != nil {
		t.Fatalf("go mod tidy: %v\n%s", err, out)
	}

	return dir
}

func TestGenerate_EmptySchema_WritesNothing(t *testing.T) {
	dir := writeGenerateFixture(t, generateFixtureSchemaEmpty)

	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()

	result, err := Generate(ctx, dir, GenerateOptions{})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if len(result.Stale) != 0 {
		t.Errorf("Stale = %v, want none", result.Stale)
	}
	if _, err := os.Stat(filepath.Join(dir, "models")); err == nil {
		t.Error("models/ directory was created for an empty schema")
	}
}

func TestGenerate_OneModel_WritesGenFile(t *testing.T) {
	dir := writeGenerateFixture(t, generateFixtureSchemaOneModel)

	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()

	result, err := Generate(ctx, dir, GenerateOptions{})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if len(result.Stale) != 1 || result.Stale[0] != filepath.Join("models", "widget.gen.go") {
		t.Fatalf("Stale = %v, want [models/widget.gen.go]", result.Stale)
	}

	entries, err := os.ReadDir(filepath.Join(dir, "models"))
	if err != nil {
		t.Fatalf("read models dir: %v", err)
	}
	if len(entries) != 1 || entries[0].Name() != "widget.gen.go" {
		t.Fatalf("models/ entries = %v, want exactly [widget.gen.go]", entries)
	}

	data, err := os.ReadFile(filepath.Join(dir, "models", "widget.gen.go"))
	if err != nil {
		t.Fatalf("read widget.gen.go: %v", err)
	}
	if !strings.HasPrefix(string(data), "// Code generated by goerp module generate. DO NOT EDIT.\n") {
		t.Errorf("widget.gen.go missing DO NOT EDIT header:\n%s", data)
	}

	formatted, err := format.Source(data)
	if err != nil {
		t.Fatalf("widget.gen.go is not valid Go: %v\n%s", err, data)
	}
	if string(formatted) != string(data) {
		t.Errorf("widget.gen.go is not gofmt-clean:\ngot:\n%s\nwant:\n%s", data, formatted)
	}
}

// Definition-only packages link no host functions, so the sandboxed schema driver can
// evaluate them.
func TestGenerate_SchemaImportingEventsDef_Succeeds(t *testing.T) {
	dir := writeGenerateFixture(t, `package schema

import (
	"github.com/djangbahevans/goerp/sdk/go/events/def"
	"github.com/djangbahevans/goerp/sdk/go/model"
)

type WidgetCreatedPayload struct {
	WidgetID string
}

var WidgetCreated = def.Define[WidgetCreatedPayload]("widgets.widget.created", def.Version(1))

var Schema = model.Schema{
	Models: []*model.ModelDeclaration{
		model.Define("widgets.widget", model.Table("widgets")).
			WithStandardFields().
			Field("name", model.Text().Required()),
	},
}
`)

	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()

	if _, err := Generate(ctx, dir, GenerateOptions{}); err != nil {
		t.Fatalf("Generate: %v", err)
	}
}

// Definition-only packages link no host functions, so the sandboxed schema driver can
// evaluate them.
func TestGenerate_SchemaImportingJobsDef_Succeeds(t *testing.T) {
	dir := writeGenerateFixture(t, `package schema

import (
	"github.com/djangbahevans/goerp/sdk/go/jobs/def"
	"github.com/djangbahevans/goerp/sdk/go/model"
)

type ImportPayload struct {
	FileID string `+"`msgpack:\"file_id\"`"+`
}

var ImportWidgets = def.Define[ImportPayload]("widgets_import", def.Label("Import widgets"), def.Queue(def.QueueBulk))

var Schema = model.Schema{
	Models: []*model.ModelDeclaration{
		model.Define("widgets.widget", model.Table("widgets")).
			WithStandardFields().
			Field("name", model.Text().Required()),
	},
}
`)

	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()

	if _, err := Generate(ctx, dir, GenerateOptions{}); err != nil {
		t.Fatalf("Generate: %v", err)
	}
}

// Definition-only packages link no host functions, so the sandboxed schema driver can
// evaluate them.
func TestGenerate_SchemaImportingConfigDef_Succeeds(t *testing.T) {
	dir := writeGenerateFixture(t, `package schema

import (
	"github.com/djangbahevans/goerp/sdk/go/config/def"
	"github.com/djangbahevans/goerp/sdk/go/model"
)

var DefaultCountry = def.String("default_country_code", "GH", def.Label("Default Country"))

var Schema = model.Schema{
	Models: []*model.ModelDeclaration{
		model.Define("widgets.widget", model.Table("widgets")).
			WithStandardFields().
			Field("name", model.Text().Required()),
	},
}
`)

	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()

	if _, err := Generate(ctx, dir, GenerateOptions{}); err != nil {
		t.Fatalf("Generate: %v", err)
	}
}

// Definition-only packages link no host functions, so the sandboxed schema driver can
// evaluate them.
func TestGenerate_SchemaImportingPerm_Succeeds(t *testing.T) {
	dir := writeGenerateFixture(t, `package schema

import (
	"github.com/djangbahevans/goerp/sdk/go/model"
	"github.com/djangbahevans/goerp/sdk/go/perm"
)

var WidgetRead = perm.Define("widgets:widget:read", perm.Description("View widgets"), perm.DefaultRoles(perm.User))

var OwnWidgets = perm.DefinePolicy("widgets:widget:own_only", WidgetRead, "record.created_by = current_user.id")

var Schema = model.Schema{
	Models: []*model.ModelDeclaration{
		model.Define("widgets.widget", model.Table("widgets")).
			WithStandardFields().
			Field("name", model.Text().Required()),
	},
}
`)

	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()

	if _, err := Generate(ctx, dir, GenerateOptions{}); err != nil {
		t.Fatalf("Generate: %v", err)
	}
}

// Cross-module reference markers avoid imports between independently built modules and are
// shared across referencing models.
func TestGenerate_CrossModuleMany2One_WritesSharedRefsFile(t *testing.T) {
	dir := writeGenerateFixtureWithManifest(t, generateFixtureSchemaCrossModuleMany2One, generateFixtureManifest("widgets", "contacts"))

	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()

	result, err := Generate(ctx, dir, GenerateOptions{})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	wantStale := []string{filepath.Join("models", "cross_module_refs.gen.go"), filepath.Join("models", "gadget.gen.go")}
	if len(result.Stale) != len(wantStale) || result.Stale[0] != wantStale[0] || result.Stale[1] != wantStale[1] {
		t.Fatalf("Stale = %v, want %v", result.Stale, wantStale)
	}

	gadgetSrc, err := os.ReadFile(filepath.Join(dir, "models", "gadget.gen.go"))
	if err != nil {
		t.Fatalf("read gadget.gen.go: %v", err)
	}
	if !strings.Contains(normalizeSpaces(string(gadgetSrc)), "Owner orm.Ref[ContactsContactRef]") {
		t.Errorf("gadget.gen.go missing orm.Ref[ContactsContactRef] expansion field:\n%s", gadgetSrc)
	}
	if strings.Contains(string(gadgetSrc), `"github.com/djangbahevans/goerp`) && strings.Contains(string(gadgetSrc), "/contacts/") {
		t.Errorf("gadget.gen.go must not import the target module's own package:\n%s", gadgetSrc)
	}

	refsSrc, err := os.ReadFile(filepath.Join(dir, "models", "cross_module_refs.gen.go"))
	if err != nil {
		t.Fatalf("read cross_module_refs.gen.go: %v", err)
	}
	if !strings.Contains(string(refsSrc), "type ContactsContactRef struct{}") {
		t.Errorf("cross_module_refs.gen.go missing marker type:\n%s", refsSrc)
	}
	if !strings.Contains(string(refsSrc), `func (ContactsContactRef) ResourceName() string { return "contacts.contact" }`) {
		t.Errorf("cross_module_refs.gen.go missing ResourceName():\n%s", refsSrc)
	}
}

func TestGenerate_CrossModuleMany2One_TargetModuleNotDeclared_Fails(t *testing.T) {
	dir := writeGenerateFixtureWithManifest(t, generateFixtureSchemaCrossModuleMany2One, generateFixtureManifest("widgets"))

	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()

	if _, err := Generate(ctx, dir, GenerateOptions{}); err == nil {
		t.Fatal("expected an error for a Many2One target whose module isn't in depends_on or soft_depends_on")
	}
}

func TestGenerate_ManifestMissingName_Fails(t *testing.T) {
	dir := writeGenerateFixtureWithManifest(t, generateFixtureSchemaOneModel, `{"depends_on": []}`)

	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()

	_, err := Generate(ctx, dir, GenerateOptions{})
	if err == nil {
		t.Fatal("expected an error for a manifest.json with no \"name\" field")
	}
	if !strings.Contains(err.Error(), "name") {
		t.Errorf("error = %q, want it to name the missing manifest field", err)
	}
}

func TestGenerate_SecondRunLeavesMtimeUnchanged(t *testing.T) {
	dir := writeGenerateFixture(t, generateFixtureSchemaOneModel)

	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()

	if _, err := Generate(ctx, dir, GenerateOptions{}); err != nil {
		t.Fatalf("first Generate: %v", err)
	}

	genFile := filepath.Join(dir, "models", "widget.gen.go")
	before, err := os.Stat(genFile)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}

	// Sleep past most filesystems' mtime resolution so a spurious
	// rewrite would actually be observable.
	time.Sleep(20 * time.Millisecond)

	result, err := Generate(ctx, dir, GenerateOptions{})
	if err != nil {
		t.Fatalf("second Generate: %v", err)
	}
	if len(result.Stale) != 0 {
		t.Errorf("second run's Stale = %v, want none", result.Stale)
	}

	after, err := os.Stat(genFile)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if !before.ModTime().Equal(after.ModTime()) {
		t.Errorf("widget.gen.go mtime changed: before=%v after=%v", before.ModTime(), after.ModTime())
	}
}

func TestGenerate_Check(t *testing.T) {
	dir := writeGenerateFixture(t, generateFixtureSchemaOneModel)

	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()

	if _, err := Generate(ctx, dir, GenerateOptions{}); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	if _, err := Generate(ctx, dir, GenerateOptions{Check: true}); err != nil {
		t.Fatalf("--check on fresh output: %v", err)
	}

	if err := os.WriteFile(filepath.Join(dir, "schema", "schema.go"), []byte(generateFixtureSchemaTwoModels), 0o644); err != nil {
		t.Fatalf("update schema.go: %v", err)
	}

	_, err := Generate(ctx, dir, GenerateOptions{Check: true})
	if err == nil {
		t.Fatal("--check after a schema change: expected a non-zero error")
	}
	if !strings.Contains(err.Error(), filepath.Join("models", "gadget.gen.go")) {
		t.Errorf("--check error = %q, want it to name models/gadget.gen.go", err)
	}

	if _, err := os.Stat(filepath.Join(dir, "models", "gadget.gen.go")); err == nil {
		t.Error("--check wrote models/gadget.gen.go — it must not write anything")
	}
}

func TestGenerate_OrphanedGenFileIsRemoved(t *testing.T) {
	dir := writeGenerateFixture(t, generateFixtureSchemaTwoModels)

	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()

	if _, err := Generate(ctx, dir, GenerateOptions{}); err != nil {
		t.Fatalf("first Generate: %v", err)
	}

	if err := os.WriteFile(filepath.Join(dir, "schema", "schema.go"), []byte(generateFixtureSchemaOneModel), 0o644); err != nil {
		t.Fatalf("update schema.go: %v", err)
	}

	result, err := Generate(ctx, dir, GenerateOptions{})
	if err != nil {
		t.Fatalf("second Generate: %v", err)
	}
	if len(result.Stale) != 1 || result.Stale[0] != filepath.Join("models", "gadget.gen.go") {
		t.Fatalf("Stale = %v, want [models/gadget.gen.go]", result.Stale)
	}

	if _, err := os.Stat(filepath.Join(dir, "models", "gadget.gen.go")); !os.IsNotExist(err) {
		t.Errorf("models/gadget.gen.go still exists after its model left the schema: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "models", "widget.gen.go")); err != nil {
		t.Errorf("models/widget.gen.go should still exist: %v", err)
	}
}

func TestGenerate_SchemaImportingModelsPackage_Fails(t *testing.T) {
	dir := writeGenerateFixture(t, generateFixtureSchemaImportingModels("generate-fixture"))

	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()

	_, err := Generate(ctx, dir, GenerateOptions{})
	if err == nil {
		t.Fatal("expected an error for a schema package importing the module's own models package")
	}
	if !strings.Contains(err.Error(), "generate-fixture/models") {
		t.Errorf("error = %q, want it to name generate-fixture/models", err)
	}
}

func TestGenerate_ResourceNameCollision_Fails(t *testing.T) {
	dir := writeGenerateFixture(t, generateFixtureSchemaResourceNameCollision)

	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()

	_, err := Generate(ctx, dir, GenerateOptions{})
	if err == nil {
		t.Fatal("expected an error for two models resolving to the same ResourceName()")
	}
	if !strings.Contains(err.Error(), "order.gen.go") {
		t.Errorf("error = %q, want it to name order.gen.go", err)
	}
	if _, statErr := os.Stat(filepath.Join(dir, "models")); statErr == nil {
		t.Error("models/ was created despite the collision error")
	}
}

func TestGenerate_PackageIdentifierCollision_Fails(t *testing.T) {
	dir := writeGenerateFixture(t, generateFixtureSchemaPackageIdentifierCollision)

	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()

	_, err := Generate(ctx, dir, GenerateOptions{})
	if err == nil {
		t.Fatal("expected an error for two models whose generated identifiers collide")
	}
	if !strings.Contains(err.Error(), "widgets.gadget") || !strings.Contains(err.Error(), "widgets.gadget_fields") || !strings.Contains(err.Error(), "GadgetFields") {
		t.Errorf("error = %q, want it to name both widgets.gadget and widgets.gadget_fields and the colliding identifier GadgetFields", err)
	}
	if _, statErr := os.Stat(filepath.Join(dir, "models")); statErr == nil {
		t.Error("models/ was created despite the collision error")
	}
}

// generateFixtureSchemaWithinModelFieldCollision has two Selection fields
// on the same model whose names pascalCase to the same Go identifier
// ("ref-type" and "ref_type" both → "RefType") — a within-model
// collision renderModelFile's own claimFieldName already catches, with a
// precise field-level message.
const generateFixtureSchemaWithinModelFieldCollision = `package schema

import "github.com/djangbahevans/goerp/sdk/go/model"

var Schema = model.Schema{
	Models: []*model.ModelDeclaration{
		model.Define("widgets.gadget", model.Table("gadgets")).
			WithStandardFields().
			Field("ref-type", model.Selection("a", "b").Required()).
			Field("ref_type", model.Selection("a", "b").Required()),
	},
}
`

// Within-model collisions need field-level errors before package-wide identifier checks to
// avoid naming the same model twice.
func TestGenerate_WithinModelFieldCollision_ReportsFieldLevelError(t *testing.T) {
	dir := writeGenerateFixture(t, generateFixtureSchemaWithinModelFieldCollision)

	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()

	_, err := Generate(ctx, dir, GenerateOptions{})
	if err == nil {
		t.Fatal("expected an error for two fields on one model colliding on the same Go identifier")
	}
	if !strings.Contains(err.Error(), `"ref-type"`) || !strings.Contains(err.Error(), `"ref_type"`) {
		t.Errorf("error = %q, want it to name both colliding fields ref-type and ref_type", err)
	}
	if strings.Contains(err.Error(), `both "widgets.gadget" and "widgets.gadget"`) {
		t.Errorf("error = %q, is the confusing self-referential model-vs-model message, not the field-level one", err)
	}
}

func TestGenerate_SchemaCompileError_SurfacesError(t *testing.T) {
	dir := writeGenerateFixture(t, generateFixtureSchemaCompileError)

	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()

	_, err := Generate(ctx, dir, GenerateOptions{})
	if err == nil {
		t.Fatal("expected an error for a schema package with a real compile error")
	}
}
