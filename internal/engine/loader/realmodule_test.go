package loader

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/config"
	"github.com/djangbahevans/goerp/internal/engine/module"
	"github.com/djangbahevans/goerp/internal/engine/wasm"
	"github.com/djangbahevans/goerp/internal/engine/wasm/wasmtest"
)

// Compile a real SDK module as a WASI reactor: c-shared preserves exports without invoking
// a command entry point.
func compileRealFixture(t *testing.T) []byte {
	t.Helper()

	wasmPath := filepath.Join(t.TempDir(), "realfixture.wasm")
	cmd := exec.Command("go", "build", "-buildmode=c-shared", "-o", wasmPath, "./testdata/realfixture")
	cmd.Env = append(os.Environ(), "GOOS=wasip1", "GOARCH=wasm")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("compile testdata/realfixture: %v\n%s", err, out)
	}

	data, err := os.ReadFile(wasmPath)
	if err != nil {
		t.Fatalf("read compiled fixture: %v", err)
	}
	return data
}

// newRealFixtureRuntime is newTestRuntime with a larger pool memory limit
// — a real Go-compiled wasip1 binary's minimum linear memory (about 2 MiB)
// is well past what the package's hand-assembled bytecode fixtures need
// (loader_test.go's newTestRuntime uses 1 MiB, sized for those).
func newRealFixtureRuntime(t *testing.T) *wasm.Runtime {
	t.Helper()
	rt, err := wasm.New(&config.Config{
		CompilationCache:  wasmtest.SharedCompilationCacheDir(),
		PoolMaxMemoryByes: 64 << 20,
		Environment:       string(config.Production),
	}, nil, nil, nil)
	if err != nil {
		t.Fatalf("wasm.New: %v", err)
	}
	t.Cleanup(func() { _ = rt.Close(context.Background()) })
	return rt
}

func TestLoadModule_RealCompiledModule_RoundTripsSDKDeclaredData(t *testing.T) {
	wasmBytes := compileRealFixture(t)
	rt := newRealFixtureRuntime(t)

	src := Source{
		Name:          "widgets",
		ManifestBytes: manifestJSON(t, "widgets", wasmBytes, []string{}),
		WasmBytes:     wasmBytes,
	}

	m := LoadModule(t.Context(), rt, testPoolCfg(), src)

	if m.Status == module.StatusFailed {
		t.Fatalf("Status = StatusFailed, FailureReason = %q", m.FailureReason)
	}
	// Registered after newRealFixtureRuntime's rt.Close cleanup, so LIFO
	// ordering drains the pool's background replenishLoop before the
	// runtime closes — otherwise the two race.
	t.Cleanup(func() { m.Pool.DrainAndClose(context.Background(), 5*time.Second) })

	if len(m.ExplicitRoutes) != 1 {
		t.Fatalf("len(ExplicitRoutes) = %d, want 1", len(m.ExplicitRoutes))
	}
	if got := m.ExplicitRoutes[0]; got.Method != "GET" || got.Path != "/widgets/ping" {
		t.Errorf("ExplicitRoutes[0] = %+v, want {Method: GET, Path: /widgets/ping}", got)
	}

	if len(m.ModelDecls) != 1 {
		t.Fatalf("len(ModelDecls) = %d, want 1", len(m.ModelDecls))
	}
	if got := m.ModelDecls[0]; got.Name != "widgets.widget" || got.Label != "Widget" {
		t.Errorf("ModelDecls[0].Name/Label = %q/%q, want %q/%q", got.Name, got.Label, "widgets.widget", "Widget")
	}
	foundNameField := false
	for _, f := range m.ModelDecls[0].Fields {
		if f.Name == "name" {
			foundNameField = true
		}
	}
	if !foundNameField {
		t.Error("expected a \"name\" field on widgets.widget, matching the fixture's own .Field(\"name\", ...) call")
	}

	if len(m.DataMigrations) != 1 {
		t.Fatalf("len(DataMigrations) = %d, want 1", len(m.DataMigrations))
	}
	if got := m.DataMigrations[0].Handler; got != "backfill_widget_name" {
		t.Errorf("DataMigrations[0].Handler = %q, want %q", got, "backfill_widget_name")
	}
}
