package moduleboot

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/config"
	"github.com/djangbahevans/goerp/internal/engine/loader"
	"github.com/djangbahevans/goerp/internal/engine/module"
	"github.com/djangbahevans/goerp/internal/engine/wasm"
)

// okModule exports allocate/deallocate/get_routes/get_model_declarations/
// get_data_migrations, where the three get_* exports return empty msgpack
// collections — the simplest module that makes every step of
// loader.LoadModule succeed (mirrors loader_test.go's own okModule).
var okModule = []byte{
	0x00, 0x61, 0x73, 0x6D, 0x01, 0x00, 0x00, 0x00, 0x01, 0x0F, 0x03, 0x60,
	0x01, 0x7F, 0x01, 0x7F, 0x60, 0x02, 0x7F, 0x7F, 0x00, 0x60, 0x00, 0x01,
	0x7E, 0x03, 0x06, 0x05, 0x00, 0x01, 0x02, 0x02, 0x02, 0x05, 0x03, 0x01,
	0x00, 0x01, 0x07, 0x55, 0x05, 0x08, 0x61, 0x6C, 0x6C, 0x6F, 0x63, 0x61,
	0x74, 0x65, 0x00, 0x00, 0x0A, 0x64, 0x65, 0x61, 0x6C, 0x6C, 0x6F, 0x63,
	0x61, 0x74, 0x65, 0x00, 0x01, 0x0A, 0x67, 0x65, 0x74, 0x5F, 0x72, 0x6F,
	0x75, 0x74, 0x65, 0x73, 0x00, 0x02, 0x16, 0x67, 0x65, 0x74, 0x5F, 0x6D,
	0x6F, 0x64, 0x65, 0x6C, 0x5F, 0x64, 0x65, 0x63, 0x6C, 0x61, 0x72, 0x61,
	0x74, 0x69, 0x6F, 0x6E, 0x73, 0x00, 0x03, 0x13, 0x67, 0x65, 0x74, 0x5F,
	0x64, 0x61, 0x74, 0x61, 0x5F, 0x6D, 0x69, 0x67, 0x72, 0x61, 0x74, 0x69,
	0x6F, 0x6E, 0x73, 0x00, 0x04, 0x0A, 0x2A, 0x05, 0x04, 0x00, 0x41, 0x00,
	0x0B, 0x02, 0x00, 0x0B, 0x0A, 0x00, 0x42, 0x81, 0x80, 0x80, 0x80, 0x80,
	0x80, 0x02, 0x0B, 0x0A, 0x00, 0x42, 0x81, 0x80, 0x80, 0x80, 0x90, 0x80,
	0x02, 0x0B, 0x0A, 0x00, 0x42, 0x81, 0x80, 0x80, 0x80, 0x80, 0x80, 0x02,
	0x0B, 0x0B, 0x0F, 0x02, 0x00, 0x41, 0x80, 0x10, 0x0B, 0x01, 0x90, 0x00,
	0x41, 0x81, 0x10, 0x0B, 0x01, 0x80,
}

func manifestJSON(t *testing.T, name string, wasmBytes []byte, dependsOn []string) []byte {
	t.Helper()
	sum := sha256.Sum256(wasmBytes)
	if dependsOn == nil {
		dependsOn = []string{}
	}

	fields := map[string]any{
		"name":         name,
		"display_name": name,
		"type":         "domain",
		"version":      "1.0.0",
		"description":  "a test module",
		"abi_version":  "1",
		"engine":       ">=0.5.0 <1.0.0",
		"depends_on":   dependsOn,
		"capabilities": []string{},
		"schema": map[string]any{
			"owned_models": []string{},
		},
		"checksum": fmt.Sprintf("sha256:%x", sum),
	}

	data, err := json.Marshal(fields)
	if err != nil {
		t.Fatalf("marshal manifest fixture: %v", err)
	}
	return data
}

func writeModuleDir(t *testing.T, root, name string, wasmBytes []byte, dependsOn []string) {
	t.Helper()
	dir := filepath.Join(root, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), manifestJSON(t, name, wasmBytes, dependsOn), 0o644); err != nil {
		t.Fatalf("write manifest.json: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "module.wasm"), wasmBytes, 0o644); err != nil {
		t.Fatalf("write module.wasm: %v", err)
	}
}

func TestDiscover_MissingDirReturnsNilNotError(t *testing.T) {
	sources, err := Discover(filepath.Join(t.TempDir(), "does-not-exist"))
	if err != nil {
		t.Fatalf("Discover() error = %v, want nil", err)
	}
	if sources != nil {
		t.Fatalf("Discover() = %+v, want nil", sources)
	}
}

func TestDiscover_ReadsEachModuleDir(t *testing.T) {
	root := t.TempDir()
	writeModuleDir(t, root, "widgets", okModule, nil)
	writeModuleDir(t, root, "gadgets", okModule, []string{"widgets"})

	sources, err := Discover(root)
	if err != nil {
		t.Fatalf("Discover() error = %v", err)
	}
	if len(sources) != 2 {
		t.Fatalf("Discover() returned %d sources, want 2", len(sources))
	}

	names := map[string]bool{}
	for _, src := range sources {
		names[src.Name] = true
		if len(src.ManifestBytes) == 0 || len(src.WasmBytes) == 0 {
			t.Errorf("source %q has empty bytes", src.Name)
		}
	}
	if !names["widgets"] || !names["gadgets"] {
		t.Fatalf("Discover() names = %v, want widgets and gadgets", names)
	}
}

func TestDiscover_SkipsDirMissingManifestOrWasm(t *testing.T) {
	root := t.TempDir()
	writeModuleDir(t, root, "widgets", okModule, nil)

	// Missing module.wasm entirely.
	noWasmDir := filepath.Join(root, "no-wasm")
	if err := os.MkdirAll(noWasmDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(noWasmDir, "manifest.json"), manifestJSON(t, "no-wasm", okModule, nil), 0o644); err != nil {
		t.Fatal(err)
	}

	// Missing manifest.json entirely.
	noManifestDir := filepath.Join(root, "no-manifest")
	if err := os.MkdirAll(noManifestDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(noManifestDir, "module.wasm"), okModule, 0o644); err != nil {
		t.Fatal(err)
	}

	sources, err := Discover(root)
	if err != nil {
		t.Fatalf("Discover() error = %v", err)
	}
	if len(sources) != 1 || sources[0].Name != "widgets" {
		t.Fatalf("Discover() = %+v, want only \"widgets\"", sources)
	}
}

func TestOrder_DependenciesPrecedeDependents(t *testing.T) {
	sources := []loader.Source{
		{Name: "gadgets", ManifestBytes: manifestJSON(t, "gadgets", okModule, []string{"widgets"})},
		{Name: "widgets", ManifestBytes: manifestJSON(t, "widgets", okModule, nil)},
		{Name: "sprockets", ManifestBytes: manifestJSON(t, "sprockets", okModule, []string{"gadgets"})},
	}

	ordered, err := Order(sources)
	if err != nil {
		t.Fatalf("Order() error = %v", err)
	}
	if len(ordered) != 3 {
		t.Fatalf("Order() returned %d sources, want 3", len(ordered))
	}

	index := make(map[string]int, len(ordered))
	for i, src := range ordered {
		index[src.Name] = i
	}
	if index["widgets"] >= index["gadgets"] {
		t.Errorf("widgets (dependency) at %d, gadgets (dependent) at %d; want widgets first", index["widgets"], index["gadgets"])
	}
	if index["gadgets"] >= index["sprockets"] {
		t.Errorf("gadgets (dependency) at %d, sprockets (dependent) at %d; want gadgets first", index["gadgets"], index["sprockets"])
	}
}

func TestOrder_CycleFailsWithNamedCycle(t *testing.T) {
	sources := []loader.Source{
		{Name: "a", ManifestBytes: manifestJSON(t, "a", okModule, []string{"b"})},
		{Name: "b", ManifestBytes: manifestJSON(t, "b", okModule, []string{"a"})},
	}

	_, err := Order(sources)
	if err == nil {
		t.Fatal("Order() error = nil, want a cycle error")
	}
	if !strings.Contains(err.Error(), "a") || !strings.Contains(err.Error(), "b") {
		t.Errorf("Order() error = %q, want it to name both modules in the cycle", err.Error())
	}
}

func TestOrder_UnknownDependencyIsNotAnOrderingError(t *testing.T) {
	sources := []loader.Source{
		{Name: "widgets", ManifestBytes: manifestJSON(t, "widgets", okModule, []string{"never-installed"})},
	}

	ordered, err := Order(sources)
	if err != nil {
		t.Fatalf("Order() error = %v, want nil for a dependency absent from sources", err)
	}
	if len(ordered) != 1 || ordered[0].Name != "widgets" {
		t.Fatalf("Order() = %+v, want just widgets", ordered)
	}
}

func newTestRuntime(t *testing.T) *wasm.Runtime {
	t.Helper()
	rt, err := wasm.New(&config.Config{
		CompilationCache:  filepath.Join(t.TempDir(), "cache"),
		PoolMaxMemoryByes: 1 << 20,
		Environment:       string(config.Production),
	}, nil)
	if err != nil {
		t.Fatalf("wasm.New: %v", err)
	}
	t.Cleanup(func() { _ = rt.Close(context.Background()) })
	return rt
}

func testPoolCfg() wasm.PoolConfig {
	return wasm.PoolConfig{MaxSize: 1, WarmSize: 0, BorrowTimeout: time.Second}
}

func TestLoadCascading_DependentOfFailedModuleIsSkipped(t *testing.T) {
	rt := newTestRuntime(t)
	garbage := []byte("not a wasm binary")

	// "widgets" fails to compile; "gadgets" depends on it and should never
	// be attempted; "standalone" has no dependency on either and should
	// load normally.
	ordered := []loader.Source{
		{Name: "widgets", ManifestBytes: manifestJSON(t, "widgets", garbage, nil), WasmBytes: garbage},
		{Name: "gadgets", ManifestBytes: manifestJSON(t, "gadgets", okModule, []string{"widgets"}), WasmBytes: okModule},
		{Name: "standalone", ManifestBytes: manifestJSON(t, "standalone", okModule, nil), WasmBytes: okModule},
	}

	modules := LoadCascading(context.Background(), rt, testPoolCfg(), ordered)

	widgets, ok := modules["widgets"]
	if !ok || widgets.Status != module.StatusFailed {
		t.Fatalf("widgets = %+v, want StatusFailed", widgets)
	}

	gadgets, ok := modules["gadgets"]
	if !ok {
		t.Fatal("expected a \"gadgets\" entry")
	}
	if gadgets.Status != module.StatusFailed {
		t.Fatalf("gadgets.Status = %v, want StatusFailed (cascaded)", gadgets.Status)
	}
	if !strings.Contains(gadgets.FailureReason, "widgets") {
		t.Errorf("gadgets.FailureReason = %q, want it to name the upstream failure", gadgets.FailureReason)
	}

	standalone, ok := modules["standalone"]
	if !ok || standalone.Status != module.StatusSyncing {
		t.Fatalf("standalone = %+v, want StatusSyncing (unaffected)", standalone)
	}
}

func TestLoadCascading_TransitiveDependentIsAlsoSkipped(t *testing.T) {
	rt := newTestRuntime(t)
	garbage := []byte("not a wasm binary")

	ordered := []loader.Source{
		{Name: "a", ManifestBytes: manifestJSON(t, "a", garbage, nil), WasmBytes: garbage},
		{Name: "b", ManifestBytes: manifestJSON(t, "b", okModule, []string{"a"}), WasmBytes: okModule},
		{Name: "c", ManifestBytes: manifestJSON(t, "c", okModule, []string{"b"}), WasmBytes: okModule},
	}

	modules := LoadCascading(context.Background(), rt, testPoolCfg(), ordered)

	if modules["a"].Status != module.StatusFailed {
		t.Fatalf("a.Status = %v, want StatusFailed", modules["a"].Status)
	}
	if modules["b"].Status != module.StatusFailed {
		t.Fatalf("b.Status = %v, want StatusFailed (cascaded from a)", modules["b"].Status)
	}
	if modules["c"].Status != module.StatusFailed {
		t.Fatalf("c.Status = %v, want StatusFailed (cascaded transitively via b)", modules["c"].Status)
	}
	if !strings.Contains(modules["c"].FailureReason, "b") {
		t.Errorf("c.FailureReason = %q, want it to name its direct upstream (b), not the root cause", modules["c"].FailureReason)
	}
}
