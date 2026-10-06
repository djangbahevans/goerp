package module

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

const testWidgetKind = "test_widget"

type testWidget struct {
	Name string `json:"name"`
	Rank int    `json:"rank"`
}

// testWidgetCollector is a declaration kind the framework knows nothing
// about: it turns test_widget declarations into the test_widgets manifest key
// and rejects a declaration named "invalid".
type testWidgetCollector struct{}

func (testWidgetCollector) Key() string { return "test_widgets" }

func (testWidgetCollector) Kinds() []string { return []string{testWidgetKind} }

func (testWidgetCollector) Collect(d Declarations, _ ModuleInfo) (any, error) {
	widgets, err := decodeDeclarations[testWidget](d, testWidgetKind)
	if err != nil || len(widgets) == 0 {
		return nil, err
	}
	for _, w := range widgets {
		if w.Name == "invalid" {
			return nil, errors.New(`widget "invalid" is not allowed`)
		}
	}
	slices.SortFunc(widgets, func(a, b testWidget) int { return strings.Compare(a.Name, b.Name) })
	return widgets, nil
}

const collectFixtureMain = `package main

import "github.com/djangbahevans/goerp/sdk/go/declare"

type widget struct {
	Name string ` + "`json:\"name\"`" + `
	Rank int    ` + "`json:\"rank\"`" + `
}

func init() {
	declare.Add("test_widget", widget{Name: "zeta", Rank: 2})
	declare.Add("test_widget", widget{Name: "alpha", Rank: 1})
}

func main() {}
`

// collectFixtureManifest keeps its own formatting and unrelated keys so a
// rewrite that touched them would show.
const collectFixtureManifest = "{\n  \"name\": \"widgets\",\n  \"version\":    \"1.0.0\",\n  \"depends_on\": [ \"core\" ]\n}\n"

func writeCollectFixture(t *testing.T, mainGo, manifest string) string {
	t.Helper()

	dir := writeGenerateFixtureWithManifest(t, generateFixtureSchemaOneModel, manifest)
	if err := os.MkdirAll(filepath.Join(dir, "cmd", "module"), 0o755); err != nil {
		t.Fatalf("mkdir cmd/module: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "cmd", "module", "main.go"), []byte(mainGo), 0o644); err != nil {
		t.Fatalf("write main.go: %v", err)
	}
	return dir
}

func generateCtx(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
	t.Cleanup(cancel)
	return ctx
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}

func TestGenerate_NewKindProducesItsBlockThroughACollector(t *testing.T) {
	withCollectors(t, testWidgetCollector{})
	dir := writeCollectFixture(t, collectFixtureMain, collectFixtureManifest)

	result, err := Generate(generateCtx(t), dir, GenerateOptions{})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if !slices.Equal(result.Blocks, []string{"test_widgets"}) {
		t.Errorf("Blocks = %v, want [test_widgets]", result.Blocks)
	}

	want := "{\n  \"name\": \"widgets\",\n  \"version\":    \"1.0.0\",\n  \"depends_on\": [ \"core\" ],\n  \"test_widgets\": [\n    {\n      \"name\": \"alpha\",\n      \"rank\": 1\n    },\n    {\n      \"name\": \"zeta\",\n      \"rank\": 2\n    }\n  ]\n}\n"
	if got := readFile(t, filepath.Join(dir, "manifest.json")); got != want {
		t.Errorf("manifest.json =\n%s\nwant\n%s", got, want)
	}
}

func TestGenerate_UpToDateManifestIsNotRewritten(t *testing.T) {
	withCollectors(t, testWidgetCollector{})
	dir := writeCollectFixture(t, collectFixtureMain, collectFixtureManifest)
	ctx := generateCtx(t)

	if _, err := Generate(ctx, dir, GenerateOptions{}); err != nil {
		t.Fatalf("first Generate: %v", err)
	}
	path := filepath.Join(dir, "manifest.json")
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(20 * time.Millisecond)

	result, err := Generate(ctx, dir, GenerateOptions{})
	if err != nil {
		t.Fatalf("second Generate: %v", err)
	}
	after, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Blocks) != 0 || !after.ModTime().Equal(before.ModTime()) {
		t.Errorf("an up-to-date manifest was rewritten: blocks %v, mtime %v -> %v", result.Blocks, before.ModTime(), after.ModTime())
	}
}

func TestGenerate_CheckReportsStaleBlocksAndWritesNothing(t *testing.T) {
	withCollectors(t, testWidgetCollector{})
	dir := writeCollectFixture(t, collectFixtureMain, collectFixtureManifest)
	ctx := generateCtx(t)

	_, err := Generate(ctx, dir, GenerateOptions{Check: true})
	if err == nil || !strings.Contains(err.Error(), "test_widgets") {
		t.Fatalf("--check on a stale manifest = %v, want an error naming test_widgets", err)
	}
	if got := readFile(t, filepath.Join(dir, "manifest.json")); got != collectFixtureManifest {
		t.Errorf("--check wrote manifest.json:\n%s", got)
	}
	if _, err := os.Stat(filepath.Join(dir, "models")); err == nil {
		t.Error("--check wrote models/")
	}

	if _, err := Generate(ctx, dir, GenerateOptions{}); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if _, err := Generate(ctx, dir, GenerateOptions{Check: true}); err != nil {
		t.Errorf("--check on fresh output: %v", err)
	}
}

func TestGenerate_CollectorFailureWritesNothing(t *testing.T) {
	withCollectors(t, testWidgetCollector{})
	invalid := strings.Replace(collectFixtureMain, `Name: "zeta"`, `Name: "invalid"`, 1)
	dir := writeCollectFixture(t, invalid, collectFixtureManifest)

	_, err := Generate(generateCtx(t), dir, GenerateOptions{})
	if err == nil || !strings.Contains(err.Error(), `widget "invalid" is not allowed`) {
		t.Fatalf("Generate = %v, want the collector's error", err)
	}
	if got := readFile(t, filepath.Join(dir, "manifest.json")); got != collectFixtureManifest {
		t.Errorf("manifest.json was written:\n%s", got)
	}
	if _, err := os.Stat(filepath.Join(dir, "models")); err == nil {
		t.Error("models/ was written although a collector failed")
	}
}

func TestGenerate_RemovesABlockWhoseDeclarationsAreGone(t *testing.T) {
	withCollectors(t, testWidgetCollector{})
	stale := "{\n  \"name\": \"widgets\",\n  \"test_widgets\": [1],\n  \"version\": \"1.0.0\"\n}\n"
	dir := writeCollectFixture(t, "package main\n\nfunc main() {}\n", stale)

	if _, err := Generate(generateCtx(t), dir, GenerateOptions{}); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if got, want := readFile(t, filepath.Join(dir, "manifest.json")), "{\n  \"name\": \"widgets\",\n  \"version\": \"1.0.0\"\n}\n"; got != want {
		t.Errorf("manifest.json = %q, want %q", got, want)
	}
}

func TestGenerate_DeclarationsRunWithoutHostFunctions(t *testing.T) {
	withCollectors(t, testWidgetCollector{})
	reachesForTheDatabase := `package main

import "github.com/djangbahevans/goerp/sdk/go/db"

func init() { _, _ = db.Exec("SELECT 1") }

func main() {}
`
	dir := writeCollectFixture(t, reachesForTheDatabase, collectFixtureManifest)

	_, err := Generate(generateCtx(t), dir, GenerateOptions{})
	if err == nil || !strings.Contains(err.Error(), "is not available while collecting declarations") {
		t.Fatalf("Generate = %v, want a host function to be unavailable", err)
	}
}

func TestGenerate_WasmFalseModuleKeepsItsHandWrittenBlocks(t *testing.T) {
	withCollectors(t, testWidgetCollector{})
	manifest := "{\n  \"name\": \"widgets\",\n  \"wasm\": false,\n  \"test_widgets\": [1]\n}\n"
	dir := writeGenerateFixtureWithManifest(t, generateFixtureSchemaOneModel, manifest)

	if _, err := Generate(generateCtx(t), dir, GenerateOptions{}); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if got := readFile(t, filepath.Join(dir, "manifest.json")); got != manifest {
		t.Errorf("manifest.json = %q, want it unchanged", got)
	}
}

func TestGenerate_MissingCmdModuleFails(t *testing.T) {
	withCollectors(t, testWidgetCollector{})
	dir := writeGenerateFixtureWithManifest(t, generateFixtureSchemaOneModel, collectFixtureManifest)
	if err := os.RemoveAll(filepath.Join(dir, "cmd")); err != nil {
		t.Fatal(err)
	}

	_, err := Generate(generateCtx(t), dir, GenerateOptions{})
	if err == nil || !strings.Contains(err.Error(), "cmd") {
		t.Fatalf("Generate = %v, want an error about the missing cmd/module package", err)
	}
}

func TestGenerate_WithoutCollectorsDoesNotBuildTheModule(t *testing.T) {
	withCollectors(t)
	brokenMain := "package main\n\nthis does not compile\n"
	dir := writeCollectFixture(t, brokenMain, collectFixtureManifest)

	if _, err := Generate(generateCtx(t), dir, GenerateOptions{}); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if !bytes.Equal([]byte(readFile(t, filepath.Join(dir, "manifest.json"))), []byte(collectFixtureManifest)) {
		t.Error("manifest.json changed with no collectors registered")
	}
}

func TestGenerate_UndeclaredKindFailsGeneration(t *testing.T) {
	withCollectors(t, testWidgetCollector{})
	misspelled := strings.ReplaceAll(collectFixtureMain, `"test_widget"`, `"test_widgit"`)
	dir := writeCollectFixture(t, misspelled, collectFixtureManifest)

	_, err := Generate(generateCtx(t), dir, GenerateOptions{})
	if err == nil || !strings.Contains(err.Error(), "test_widgit have no collector") {
		t.Fatalf("Generate = %v, want the unclaimed kind to be named", err)
	}
}

// TestGenerate_CollectionBuildSeesTheModelsAboutToBeWritten pins that a
// cmd/module package importing the generated models package builds before
// those models exist on disk, without Generate writing them early.
func TestGenerate_CollectionBuildSeesTheModelsAboutToBeWritten(t *testing.T) {
	// The models package links the SDK's own event definitions, so the emits
	// collector that claims that kind is registered too.
	withCollectors(t, testWidgetCollector{}, emitsCollector{})
	usesModels := `package main

import (
	"github.com/djangbahevans/goerp/sdk/go/declare"

	"generate-fixture/models"
)

func init() {
	declare.Add("test_widget", map[string]any{"name": models.WidgetFields.Name.Name(), "rank": 1})
}

func main() {}
`
	dir := writeCollectFixture(t, usesModels, collectFixtureManifest)

	if _, err := Generate(generateCtx(t), dir, GenerateOptions{Check: true}); err == nil || strings.Contains(err.Error(), "undefined") {
		t.Fatalf("--check = %v, want a stale error and a successful collection build", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "models")); err == nil {
		t.Fatal("--check wrote models/")
	}

	if _, err := Generate(generateCtx(t), dir, GenerateOptions{}); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if got := readFile(t, filepath.Join(dir, "manifest.json")); !strings.Contains(got, `"name": "name"`) {
		t.Errorf("manifest.json = %s, want the widget declared from models.WidgetFields", got)
	}
}

func TestGenerate_OutputPrintedByInitIsNotPartOfTheDeclarations(t *testing.T) {
	withCollectors(t, testWidgetCollector{})
	printing := strings.Replace(collectFixtureMain, "func init() {", "func init() {\n\tprintln(\"stderr noise\")\n\tos.Stdout.WriteString(\"stdout noise\\n\")\n", 1)
	printing = strings.Replace(printing, `import "github.com/djangbahevans/goerp/sdk/go/declare"`, "import (\n\t\"os\"\n\n\t\"github.com/djangbahevans/goerp/sdk/go/declare\"\n)", 1)
	dir := writeCollectFixture(t, printing, collectFixtureManifest)

	if _, err := Generate(generateCtx(t), dir, GenerateOptions{}); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if got := readFile(t, filepath.Join(dir, "manifest.json")); !strings.Contains(got, `"alpha"`) {
		t.Errorf("manifest.json = %s, want the declared widgets", got)
	}
}

func TestGenerate_DirReachedThroughSymlinkWithCollectors(t *testing.T) {
	withCollectors(t, testWidgetCollector{})
	dir := writeCollectFixture(t, collectFixtureMain, collectFixtureManifest)

	link := filepath.Join(t.TempDir(), "linked")
	if err := os.Symlink(dir, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	if _, err := Generate(generateCtx(t), link, GenerateOptions{}); err != nil {
		t.Fatalf("Generate through a symlink: %v", err)
	}
	if got := readFile(t, filepath.Join(dir, "manifest.json")); !strings.Contains(got, `"test_widgets"`) {
		t.Errorf("manifest.json = %s, want the generated block", got)
	}
}

func TestGenerate_ManifestRewriteKeepsModeAndLeavesNoTemporaryFile(t *testing.T) {
	withCollectors(t, testWidgetCollector{})
	dir := writeCollectFixture(t, collectFixtureMain, collectFixtureManifest)
	path := filepath.Join(dir, "manifest.json")
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := Generate(generateCtx(t), dir, GenerateOptions{}); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Errorf("manifest.json mode = %v, %v; want 0600", info.Mode().Perm(), err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.Contains(e.Name(), ".tmp-") {
			t.Errorf("temporary file %s left behind", e.Name())
		}
	}
}
