package cli

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const generateFixtureSchemaGo = `package schema

import "github.com/djangbahevans/goerp/sdk/go/model"

var Schema = model.Schema{
	Models: []*model.ModelDeclaration{
		model.Define("widgets.widget", model.Table("widgets")).
			WithStandardFields().
			Field("name", model.Text().Required()),
	},
}
`

// writeGenerateFixture scaffolds a standalone module and overlays a
// go.work workspace onto this repo's own checkout, so the fixture's
// schema package (which imports sdk/go/model) resolves without a
// require in its own go.mod — the same test-only resolution
// internal/module/generate_test.go's own writeGenerateFixture uses.
func writeGenerateFixture(t *testing.T) string {
	t.Helper()

	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("resolve repo root: %v", err)
	}

	dir := filepath.Join(t.TempDir(), "demo_module")
	if err := os.MkdirAll(filepath.Join(dir, "schema"), 0o755); err != nil {
		t.Fatalf("mkdir schema: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module generate-cli-fixture\n\ngo 1.27.0\n"), 0o644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "schema", "schema.go"), []byte(generateFixtureSchemaGo), 0o644); err != nil {
		t.Fatalf("write schema.go: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	workInit := exec.CommandContext(ctx, "go", "work", "init", ".", repoRoot)
	workInit.Dir = dir
	if out, err := workInit.CombinedOutput(); err != nil {
		t.Fatalf("go work init: %v\n%s", err, out)
	}

	return dir
}

func TestModuleGenerate_TooManyArgsIsUsageError(t *testing.T) {
	code, _, stderr := runCLI(t, "module", "generate", "a", "b")

	if code != 2 {
		t.Fatalf("exit code = %d, want 2 (usage error, cli-reference.md §2b)", code)
	}
	if !strings.Contains(stderr, "Usage:") {
		t.Errorf("stderr = %q, want the command's usage synopsis for a usage error", stderr)
	}
}

func TestModuleGenerate_WritesGenFile(t *testing.T) {
	dir := writeGenerateFixture(t)

	code, stdout, stderr := runCLI(t, "module", "generate", dir)

	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr: %s)", code, stderr)
	}
	if !strings.Contains(stdout, filepath.Join("models", "widget.gen.go")) {
		t.Errorf("stdout = %q, want it to mention models/widget.gen.go", stdout)
	}
	if _, err := os.Stat(filepath.Join(dir, "models", "widget.gen.go")); err != nil {
		t.Errorf("expected models/widget.gen.go: %v", err)
	}
}

func TestModuleGenerate_CheckFailsAfterSchemaChange(t *testing.T) {
	dir := writeGenerateFixture(t)

	if code, _, stderr := runCLI(t, "module", "generate", dir); code != 0 {
		t.Fatalf("initial generate: exit code = %d, stderr: %s", code, stderr)
	}

	if code, _, stderr := runCLI(t, "module", "generate", dir, "--check"); code != 0 {
		t.Fatalf("--check on fresh output: exit code = %d, stderr: %s", code, stderr)
	}

	changedSchema := strings.Replace(generateFixtureSchemaGo,
		`Field("name", model.Text().Required()),
	},`,
		`Field("name", model.Text().Required()),
		model.Define("widgets.gadget").WithStandardFields(),
	},`,
		1)
	if changedSchema == generateFixtureSchemaGo {
		t.Fatal("test fixture bug: schema replacement matched nothing")
	}
	if err := os.WriteFile(filepath.Join(dir, "schema", "schema.go"), []byte(changedSchema), 0o644); err != nil {
		t.Fatalf("update schema.go: %v", err)
	}

	code, _, stderr := runCLI(t, "module", "generate", dir, "--check")
	if code == 0 {
		t.Fatal("--check after a schema change: expected a non-zero exit code")
	}
	if !strings.Contains(stderr, "gadget.gen.go") {
		t.Errorf("stderr = %q, want it to name the stale models/gadget.gen.go", stderr)
	}
}
