package loader

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/config"
	"github.com/djangbahevans/goerp/internal/engine/module"
	"github.com/djangbahevans/goerp/internal/engine/wasm"
)

// bareModule is a minimal valid WASM module with no sections at all — no
// memory, no exports. Enough to compile and instantiate, but every
// get_*/deallocate export lookup on it fails, so LoadModule always fails
// after pool creation against it — exactly the failure window that used to
// leak the pool's replenishLoop goroutine.
var bareModule = []byte{0x00, 0x61, 0x73, 0x6D, 0x01, 0x00, 0x00, 0x00}

// okModule exports allocate/deallocate/get_routes/get_model_declarations/
// get_data_migrations, where the three get_* exports return empty msgpack
// collections (fixarray/fixmap, 0 elements) — the simplest module that
// makes every step of LoadModule succeed.
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

// oneRouteModule is okModule with get_routes returning one route,
// {"method":"GET","path":"/"} — used to exercise real msgpack decoding of
// ExplicitRoutes, and (loaded under a reserved module name) to exercise a
// module whose own route registration fails.
var oneRouteModule = []byte{
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
	0x0B, 0x02, 0x00, 0x0B, 0x0A, 0x00, 0x42, 0x94, 0x80, 0x80, 0x80, 0x80,
	0x80, 0x03, 0x0B, 0x0A, 0x00, 0x42, 0x81, 0x80, 0x80, 0x80, 0xC0, 0x82,
	0x03, 0x0B, 0x0A, 0x00, 0x42, 0x81, 0x80, 0x80, 0x80, 0xD0, 0x82, 0x03,
	0x0B, 0x0B, 0x29, 0x03, 0x00, 0x41, 0x80, 0x18, 0x0B, 0x14, 0x91, 0x82,
	0xA6, 0x6D, 0x65, 0x74, 0x68, 0x6F, 0x64, 0xA3, 0x47, 0x45, 0x54, 0xA4,
	0x70, 0x61, 0x74, 0x68, 0xA1, 0x2F, 0x00, 0x41, 0x94, 0x18, 0x0B, 0x01,
	0x80, 0x00, 0x41, 0x95, 0x18, 0x0B, 0x01, 0x90,
}

// manifestJSON builds a minimal valid manifest (manifest-spec.md §2's
// required root fields) whose checksum matches wasmBytes, so
// verifyChecksum passes.
func manifestJSON(t *testing.T, name string, wasmBytes []byte, capabilities []string) []byte {
	t.Helper()
	sum := sha256.Sum256(wasmBytes)

	fields := map[string]any{
		"name":         name,
		"display_name": name,
		"type":         "domain",
		"version":      "1.0.0",
		"description":  "a test module",
		"abi_version":  "1",
		"engine":       ">=0.5.0 <1.0.0",
		"depends_on":   []string{},
		"capabilities": capabilities,
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

func newTestRuntime(t *testing.T) *wasm.Runtime {
	t.Helper()
	rt, err := wasm.New(&config.Config{
		CompilationCache:  filepath.Join(t.TempDir(), "cache"),
		PoolMaxMemoryByes: 1 << 20,
		Environment:       string(config.Production),
	})
	if err != nil {
		t.Fatalf("wasm.New: %v", err)
	}
	t.Cleanup(func() { _ = rt.Close(context.Background()) })
	return rt
}

func testPoolCfg() wasm.PoolConfig {
	return wasm.PoolConfig{MaxSize: 1, WarmSize: 0, BorrowTimeout: time.Second}
}

func TestLoadModule_Success(t *testing.T) {
	rt := newTestRuntime(t)
	src := Source{
		Name:          "widgets",
		ManifestBytes: manifestJSON(t, "widgets", okModule, []string{"db.read"}),
		WasmBytes:     okModule,
	}

	m := LoadModule(context.Background(), rt, testPoolCfg(), src)

	if m.Status != module.StatusSyncing {
		t.Fatalf("Status = %v, want StatusSyncing; FailureReason = %q", m.Status, m.FailureReason)
	}
	if m.Pool == nil {
		t.Error("expected a non-nil Pool")
	}
	if m.Capabilities == 0 {
		t.Error("expected resolved capabilities to be non-zero")
	}
	if m.LoadedAt.IsZero() {
		t.Error("expected LoadedAt to be set")
	}
	if len(m.ExplicitRoutes) != 0 || len(m.ModelDecls) != 0 || len(m.DataMigrations) != 0 {
		t.Errorf("expected empty ExplicitRoutes/ModelDecls/DataMigrations, got %+v", m)
	}
}

func TestLoadModule_DecodesRealRouteFromGetRoutes(t *testing.T) {
	rt := newTestRuntime(t)
	src := Source{
		Name:          "widgets",
		ManifestBytes: manifestJSON(t, "widgets", oneRouteModule, []string{"db.read"}),
		WasmBytes:     oneRouteModule,
	}

	m := LoadModule(context.Background(), rt, testPoolCfg(), src)

	if m.Status != module.StatusSyncing {
		t.Fatalf("Status = %v, want StatusSyncing; FailureReason = %q", m.Status, m.FailureReason)
	}
	if len(m.ExplicitRoutes) != 1 {
		t.Fatalf("ExplicitRoutes = %+v, want 1 entry", m.ExplicitRoutes)
	}
	if m.ExplicitRoutes[0].Method != "GET" || m.ExplicitRoutes[0].Path != "/" {
		t.Errorf("ExplicitRoutes[0] = %+v, want Method=GET Path=/", m.ExplicitRoutes[0])
	}
}

func TestLoadModule_InvalidManifestFails(t *testing.T) {
	rt := newTestRuntime(t)
	src := Source{
		Name:          "widgets",
		ManifestBytes: []byte("not json"),
		WasmBytes:     okModule,
	}

	m := LoadModule(context.Background(), rt, testPoolCfg(), src)

	if m.Status != module.StatusFailed {
		t.Fatalf("Status = %v, want StatusFailed", m.Status)
	}
	if !strings.Contains(m.FailureReason, "manifest") {
		t.Errorf("FailureReason = %q, want it to mention the manifest", m.FailureReason)
	}
}

func TestLoadModule_ChecksumMismatchFails(t *testing.T) {
	rt := newTestRuntime(t)
	manifestBytes := manifestJSON(t, "widgets", okModule, []string{"db.read"})
	// Corrupt the binary after the manifest's checksum was computed from it.
	corrupted := append([]byte(nil), okModule...)
	corrupted[len(corrupted)-1] ^= 0xFF

	src := Source{Name: "widgets", ManifestBytes: manifestBytes, WasmBytes: corrupted}
	m := LoadModule(context.Background(), rt, testPoolCfg(), src)

	if m.Status != module.StatusFailed {
		t.Fatalf("Status = %v, want StatusFailed", m.Status)
	}
	if !strings.Contains(m.FailureReason, "checksum") {
		t.Errorf("FailureReason = %q, want it to mention the checksum", m.FailureReason)
	}
}

func TestLoadModule_UnknownCapabilityFails(t *testing.T) {
	rt := newTestRuntime(t)
	src := Source{
		Name:          "widgets",
		ManifestBytes: manifestJSON(t, "widgets", okModule, []string{"not.a.real.capability"}),
		WasmBytes:     okModule,
	}

	m := LoadModule(context.Background(), rt, testPoolCfg(), src)

	if m.Status != module.StatusFailed {
		t.Fatalf("Status = %v, want StatusFailed", m.Status)
	}
	if !strings.Contains(m.FailureReason, "capabilit") {
		t.Errorf("FailureReason = %q, want it to mention capabilities", m.FailureReason)
	}
}

func TestLoadModule_CompileFailureFails(t *testing.T) {
	rt := newTestRuntime(t)
	garbage := []byte("not a wasm binary")
	src := Source{
		Name:          "widgets",
		ManifestBytes: manifestJSON(t, "widgets", garbage, []string{"db.read"}),
		WasmBytes:     garbage,
	}

	m := LoadModule(context.Background(), rt, testPoolCfg(), src)

	if m.Status != module.StatusFailed {
		t.Fatalf("Status = %v, want StatusFailed", m.Status)
	}
}

func TestLoadAll_SecondModuleFailureLeavesFirstHealthy(t *testing.T) {
	rt := newTestRuntime(t)
	sources := []Source{
		{
			Name:          "widgets",
			ManifestBytes: manifestJSON(t, "widgets", okModule, []string{"db.read"}),
			WasmBytes:     okModule,
		},
		{
			// Named "auth" so its single "/" route expands to "/auth" —
			// a reserved engine namespace (route.RegisterModuleRoutes) —
			// making this module's own route registration fail.
			Name:          "auth",
			ManifestBytes: manifestJSON(t, "auth", oneRouteModule, []string{"db.read"}),
			WasmBytes:     oneRouteModule,
		},
	}

	modules := LoadAll(context.Background(), rt, testPoolCfg(), sources)

	widgets, ok := modules["widgets"]
	if !ok {
		t.Fatal("expected a \"widgets\" entry")
	}
	if widgets.Status != module.StatusSyncing {
		t.Errorf("widgets.Status = %v, want StatusSyncing; FailureReason = %q", widgets.Status, widgets.FailureReason)
	}

	auth, ok := modules["auth"]
	if !ok {
		t.Fatal("expected an \"auth\" entry")
	}
	if auth.Status != module.StatusFailed {
		t.Errorf("auth.Status = %v, want StatusFailed", auth.Status)
	}
	if !strings.Contains(auth.FailureReason, "reserved") {
		t.Errorf("auth.FailureReason = %q, want it to mention the reserved namespace", auth.FailureReason)
	}
}

func TestLoadModule_FailureAfterPoolCreationClosesThePool(t *testing.T) {
	rt := newTestRuntime(t)
	src := Source{
		Name:          "widgets",
		ManifestBytes: manifestJSON(t, "widgets", bareModule, []string{"db.read"}),
		WasmBytes:     bareModule,
	}

	m := LoadModule(context.Background(), rt, testPoolCfg(), src)

	if m.Status != module.StatusFailed {
		t.Fatalf("Status = %v, want StatusFailed; FailureReason = %q", m.Status, m.FailureReason)
	}
	if m.Pool == nil {
		t.Fatal("expected a non-nil Pool even on failure")
	}

	// DrainAndClose sets the pool draining before anything else — Borrow
	// returning ErrPoolDraining immediately (rather than trying to
	// instantiate or blocking on BorrowTimeout) confirms the pool was
	// actually closed on this failure path, not merely abandoned with its
	// replenishLoop goroutine still running.
	_, err := m.Pool.Borrow(context.Background())
	if !errors.Is(err, wasm.ErrPoolDraining) {
		t.Errorf("Borrow() error = %v, want %v (pool should have been closed on load failure)", err, wasm.ErrPoolDraining)
	}
}
