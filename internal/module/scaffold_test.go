package module

import (
	"context"
	"encoding/json/v2"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/config"
	"github.com/djangbahevans/goerp/internal/engine/loader"
	"github.com/djangbahevans/goerp/internal/engine/manifest"
	enginemodule "github.com/djangbahevans/goerp/internal/engine/module"
	"github.com/djangbahevans/goerp/internal/engine/moduleboot"
	"github.com/djangbahevans/goerp/internal/engine/wasm"
)

func TestCreateScaffoldsExpectedLayout(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "demo_module")

	if err := Create(dir, "demo_module", "domain", "", ""); err != nil {
		t.Fatalf("Create: %v", err)
	}

	for _, path := range []string{
		"go.mod",
		"manifest.json",
		filepath.Join("cmd", "module", "main.go"),
		filepath.Join("schema", "schema.go"),
		filepath.Join("translations", "en.json"),
	} {
		if _, err := os.Stat(filepath.Join(dir, path)); err != nil {
			t.Errorf("expected %s to exist: %v", path, err)
		}
	}

	if info, err := os.Stat(filepath.Join(dir, "internal")); err != nil || !info.IsDir() {
		t.Errorf("expected internal/ directory to exist")
	}
}

// TestCreateScaffoldsCompilableSchemaPackage is goerp#958's own acceptance
// criterion: a freshly scaffolded module's schema/schema.go compiles and
// cmd/module/main.go imports it and calls engine.WriteModels(schema.Schema).
func TestCreateScaffoldsCompilableSchemaPackage(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	dir := createInWorkspace(ctx, t)

	cmd := exec.CommandContext(ctx, "go", "build", "-buildmode=c-shared", "-o", os.DevNull, "./cmd/module")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOOS=wasip1", "GOARCH=wasm")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go build ./cmd/module: %v\n%s", err, out)
	}
}

// A scaffolded module must survive the engine's real load path, not just
// compile: the loader invokes exports the module has to declare itself.
func TestCreateScaffoldsModuleTheLoaderAccepts(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
	defer cancel()
	dir := createInWorkspace(ctx, t)

	pkg, err := Package(ctx, dir, PackageOptions{Output: filepath.Join(t.TempDir(), "demo_module.erp"), SkipFrontend: true})
	if err != nil {
		t.Fatalf("Package: %v", err)
	}
	src, err := moduleboot.DiscoverOne(pkg.ArchivePath)
	if err != nil || src == nil {
		t.Fatalf("DiscoverOne(%s) = %v, %v", pkg.ArchivePath, src, err)
	}

	rt, err := wasm.New(&config.Config{
		CompilationCache:  filepath.Join(t.TempDir(), "cache"),
		PoolMaxMemoryByes: 64 << 20,
		Environment:       string(config.Production),
	}, nil, nil, nil)
	if err != nil {
		t.Fatalf("wasm.New: %v", err)
	}
	t.Cleanup(func() { _ = rt.Close(context.Background()) })

	m := loader.LoadModule(ctx, rt, wasm.PoolConfig{MaxSize: 1, BorrowTimeout: time.Second}, *src)
	if m.Status == enginemodule.StatusFailed {
		t.Fatalf("scaffolded module failed to load: %s", m.FailureReason)
	}
	// Registered after rt.Close so LIFO cleanup drains the pool first.
	t.Cleanup(func() { m.Pool.DrainAndClose(context.Background(), 5*time.Second) })
}

// createInWorkspace scaffolds a module with no pinned SDK version and adds a
// go.work over it and this repo's checkout, so the SDK import resolves to
// the code under test rather than a published version.
func createInWorkspace(ctx context.Context, t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go toolchain not available")
	}

	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("resolve repo root: %v", err)
	}

	dir := filepath.Join(t.TempDir(), "demo_module")
	if err := Create(dir, "demo_module", "domain", "github.com/acmecorp", ""); err != nil {
		t.Fatalf("Create: %v", err)
	}

	workInit := exec.CommandContext(ctx, "go", "work", "init", ".", repoRoot)
	workInit.Dir = dir
	if out, err := workInit.CombinedOutput(); err != nil {
		t.Fatalf("go work init: %v\n%s", err, out)
	}

	return dir
}

func TestCreateGoModUsesOrgPrefix(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "demo_module")

	if err := Create(dir, "demo_module", "domain", "github.com/acmecorp", ""); err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, err := os.ReadFile(filepath.Join(dir, "go.mod"))
	if err != nil {
		t.Fatalf("read go.mod: %v", err)
	}

	want := "module github.com/acmecorp/demo_module\n"
	if len(got) < len(want) || string(got[:len(want)]) != want {
		t.Errorf("go.mod = %q, want it to start with %q", got, want)
	}
}

// TestCreateManifestPassesRealLoader is the acceptance criterion from issue
// #26 stated as a test: `goerp module create demo` must produce a manifest
// that the actual manifest loader accepts, not just well-formed JSON.
func TestCreateManifestPassesRealLoader(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "demo_module")

	if err := Create(dir, "demo_module", "domain", "", ""); err != nil {
		t.Fatalf("Create: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		t.Fatalf("read manifest.json: %v", err)
	}

	if _, err := manifest.Load(data); err != nil {
		t.Fatalf("scaffolded manifest failed to load: %v", err)
	}
}

func TestGoModContentPinsSDKVersion(t *testing.T) {
	got := string(goModContent("github.com/acmecorp/demo_module", "v0.3.0"))
	want := "module github.com/acmecorp/demo_module\n\ngo " + scaffoldGoVersion + "\n\nrequire github.com/djangbahevans/goerp v0.3.0\n"
	if got != want {
		t.Errorf("goModContent = %q, want %q", got, want)
	}
}

func TestGoModContentOmitsRequireWithoutSDKVersion(t *testing.T) {
	if got := string(goModContent("demo_module", "")); strings.Contains(got, "require") {
		t.Errorf("goModContent with no SDK version = %q, want no require", got)
	}
}

func TestManifestTemplateOwnsNoModelsByDefault(t *testing.T) {
	data, err := encodeManifest(manifestTemplate("demo_module", "domain"))
	if err != nil {
		t.Fatalf("encodeManifest: %v", err)
	}

	var decoded map[string]any
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	schema, ok := decoded["schema"].(map[string]any)
	if !ok {
		t.Fatalf("schema field missing or wrong type: %#v", decoded["schema"])
	}

	if _, ok := schema["owned_models"]; !ok {
		t.Errorf("schema.owned_models key is missing (manifest-spec.md §2 requires it)")
	}
}

// TestEncodeManifestIsDeterministic guards against encoding/json/v2's
// default map key ordering, which is randomized per call (unlike v1, which
// always sorted) — without Deterministic(true), scaffolding or patching the
// same manifest twice would produce different bytes each time.
func TestEncodeManifestIsDeterministic(t *testing.T) {
	v := manifestTemplate("demo_module", "domain")

	first, err := encodeManifest(v)
	if err != nil {
		t.Fatalf("encodeManifest: %v", err)
	}

	for range 10 {
		got, err := encodeManifest(v)
		if err != nil {
			t.Fatalf("encodeManifest: %v", err)
		}
		if string(got) != string(first) {
			t.Fatalf("encodeManifest is nondeterministic:\nfirst: %s\ngot:   %s", first, got)
		}
	}
}

func TestEncodeManifestEndsWithNewline(t *testing.T) {
	data, err := encodeManifest(manifestTemplate("demo_module", "domain"))
	if err != nil {
		t.Fatalf("encodeManifest: %v", err)
	}

	if len(data) == 0 || data[len(data)-1] != '\n' {
		t.Errorf("encodeManifest output does not end with a newline: %q", data)
	}
}

func TestDisplayName(t *testing.T) {
	tests := []struct {
		name string
		want string
	}{
		{"demo_module", "Demo Module"},
		{"contacts", "Contacts"},
		{"l10n_ghana", "L10n Ghana"},
		{"a_b_c", "A B C"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := displayName(tt.name); got != tt.want {
				t.Errorf("displayName(%q) = %q, want %q", tt.name, got, tt.want)
			}
		})
	}
}
