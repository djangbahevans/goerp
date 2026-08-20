package adminapi

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/abi"
	"github.com/djangbahevans/goerp/internal/engine/config"
	"github.com/djangbahevans/goerp/internal/engine/db"
	"github.com/djangbahevans/goerp/internal/engine/manifest"
	"github.com/djangbahevans/goerp/internal/engine/module"
	"github.com/djangbahevans/goerp/internal/engine/registry"
	"github.com/djangbahevans/goerp/internal/engine/tenant"
	"github.com/djangbahevans/goerp/internal/engine/wasm"
)

// compileActivityFixture compiles testdata/activityfixture — a real module
// built on the actual SDK (engine.OnActivity/engine.DispatchActivity), not
// hand-assembled bytecode — to wasip1 WASM, the same way loader's
// realmodule_test.go compiles testdata/realfixture.
func compileActivityFixture(t *testing.T) []byte {
	t.Helper()

	wasmPath := filepath.Join(t.TempDir(), "activityfixture.wasm")
	cmd := exec.Command("go", "build", "-buildmode=c-shared", "-o", wasmPath, "./testdata/activityfixture")
	cmd.Env = append(os.Environ(), "GOOS=wasip1", "GOARCH=wasm")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("compile testdata/activityfixture: %v\n%s", err, out)
	}

	data, err := os.ReadFile(wasmPath)
	if err != nil {
		t.Fatalf("read compiled fixture: %v", err)
	}
	return data
}

// newActivityDispatchMux compiles and loads testdata/activityfixture into a
// real wasm.Runtime/InstancePool, registers it in a ModuleRegistry, and
// wires the route onto a fresh mux.
func newActivityDispatchMux(t *testing.T) (*http.ServeMux, *tenant.Store, *sql.DB) {
	t.Helper()
	ctx := context.Background()

	conn, err := db.New(localPostgresDSN)
	if err != nil {
		t.Skipf("postgres not reachable at %s (start compose.dev.yml): %v", localPostgresDSN, err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	tenants := tenant.NewStore(conn)
	if err := tenants.Bootstrap(ctx); err != nil {
		t.Fatalf("Bootstrap() error: %v", err)
	}

	wasmBytes := compileActivityFixture(t)

	rt, err := wasm.New(&config.Config{
		CompilationCache:  filepath.Join(t.TempDir(), "cache"),
		PoolMaxMemoryByes: 64 << 20,
		Environment:       string(config.Production),
	}, nil, nil)
	if err != nil {
		t.Fatalf("wasm.New: %v", err)
	}
	t.Cleanup(func() { _ = rt.Close(ctx) })

	compiled, err := rt.CompileModule(ctx, wasmBytes)
	if err != nil {
		t.Fatalf("CompileModule: %v", err)
	}
	t.Cleanup(func() { _ = compiled.Close(ctx) })

	pool := rt.NewPool("activityfixture", compiled, wasm.PoolConfig{MaxSize: 1, WarmSize: 1, BorrowTimeout: time.Second})
	// Registered after rt.Close's cleanup so LIFO drains the pool first.
	t.Cleanup(func() { pool.DrainAndClose(ctx, 5*time.Second) })

	reg := &registry.ModuleRegistry{}
	if _, err := reg.Update(map[string]*module.LoadedModule{
		"activityfixture": {
			Manifest:     manifest.Manifest{Name: "activityfixture", Type: "standard"},
			Pool:         pool,
			Capabilities: abi.CapabilitySet(0),
			Status:       module.StatusReady,
		},
	}); err != nil {
		t.Fatalf("registry Update: %v", err)
	}

	mux := http.NewServeMux()
	RegisterActivityDispatchRoute(mux, ActivityDispatchDeps{
		Registry:  reg,
		Tenants:   tenants,
		TxLimiter: rt.TxLimiter(),
	})

	return mux, tenants, conn
}

func createTestTenant(t *testing.T, tenants *tenant.Store, conn *sql.DB, slug string) *tenant.Tenant {
	t.Helper()
	tn, err := tenants.CreateTenant(context.Background(), slug, slug)
	if err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	t.Cleanup(func() {
		_, _ = conn.Exec("DELETE FROM system.tenants WHERE id = $1", tn.ID)
	})
	return tn
}

func postActivityDispatch(t *testing.T, mux *http.ServeMux, body any) *httptest.ResponseRecorder {
	t.Helper()
	buf, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal request body: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/admin/_internal/activity-dispatch", bytes.NewReader(buf))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	return w
}

func TestActivityDispatch_SuccessRoundTripsJSONThroughWASM(t *testing.T) {
	mux, tenants, conn := newActivityDispatchMux(t)
	tn := createTestTenant(t, tenants, conn, "acme-dispatch")

	w := postActivityDispatch(t, mux, map[string]any{
		"module":      "activityfixture",
		"activity":    "reserve_inventory",
		"payload":     map[string]any{"order_id": "ord-1"},
		"tenant_id":   tn.ID,
		"workflow_id": "wf-1",
		"run_id":      "run-1",
		"attempt":     1,
	})

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body = %s", w.Code, http.StatusOK, w.Body.String())
	}

	env := decodeEnvelope(t, w)
	if env.Error != nil {
		t.Fatalf("envelope.Error = %+v, want nil", env.Error)
	}

	data, ok := env.Data.(map[string]any)
	if !ok {
		t.Fatalf("envelope.Data = %#v, want a JSON object", env.Data)
	}
	if errStr, _ := data["error"].(string); errStr != "" {
		t.Fatalf("data.error = %q, want empty", errStr)
	}
	output, ok := data["output"].(map[string]any)
	if !ok {
		t.Fatalf("data.output = %#v, want a JSON object", data["output"])
	}
	if got := output["reservation_id"]; got != "res-ord-1" {
		t.Errorf("output.reservation_id = %v, want %q", got, "res-ord-1")
	}
}

func TestActivityDispatch_ActivityBusinessErrorIsHTTP200(t *testing.T) {
	mux, tenants, conn := newActivityDispatchMux(t)
	tn := createTestTenant(t, tenants, conn, "acme-dispatch-err")

	w := postActivityDispatch(t, mux, map[string]any{
		"module":      "activityfixture",
		"activity":    "reserve_inventory",
		"payload":     map[string]any{"order_id": ""},
		"tenant_id":   tn.ID,
		"workflow_id": "wf-2",
		"run_id":      "run-2",
		"attempt":     1,
	})

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body = %s", w.Code, http.StatusOK, w.Body.String())
	}

	env := decodeEnvelope(t, w)
	data, ok := env.Data.(map[string]any)
	if !ok {
		t.Fatalf("envelope.Data = %#v, want a JSON object", env.Data)
	}
	if nonRetryable, _ := data["non_retryable"].(bool); !nonRetryable {
		t.Errorf("data.non_retryable = %v, want true", data["non_retryable"])
	}
	if errType, _ := data["error_type"].(string); errType != "invalid_order" {
		t.Errorf("data.error_type = %q, want %q", errType, "invalid_order")
	}
}

func TestActivityDispatch_UnknownModuleIsNotFound(t *testing.T) {
	mux, tenants, conn := newActivityDispatchMux(t)
	tn := createTestTenant(t, tenants, conn, "acme-dispatch-nomod")

	w := postActivityDispatch(t, mux, map[string]any{
		"module":    "does_not_exist",
		"activity":  "reserve_inventory",
		"tenant_id": tn.ID,
	})

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d; body = %s", w.Code, http.StatusNotFound, w.Body.String())
	}
	env := decodeEnvelope(t, w)
	if env.Error == nil || env.Error.Code != "module_not_found" {
		t.Errorf("envelope.Error = %+v, want code module_not_found", env.Error)
	}
}

func TestActivityDispatch_UnknownTenantIsNotFound(t *testing.T) {
	mux, _, _ := newActivityDispatchMux(t)

	w := postActivityDispatch(t, mux, map[string]any{
		"module":    "activityfixture",
		"activity":  "reserve_inventory",
		"tenant_id": "00000000-0000-0000-0000-000000000000",
	})

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d; body = %s", w.Code, http.StatusNotFound, w.Body.String())
	}
	env := decodeEnvelope(t, w)
	if env.Error == nil || env.Error.Code != "tenant_not_found" {
		t.Errorf("envelope.Error = %+v, want code tenant_not_found", env.Error)
	}
}

// Regression test: a module that failed to load still publishes with Pool
// nil (loader.LoadModule), and dispatching to it must not panic.
func TestActivityDispatch_FailedModuleReturnsCleanError(t *testing.T) {
	conn, err := db.New(localPostgresDSN)
	if err != nil {
		t.Skipf("postgres not reachable at %s (start compose.dev.yml): %v", localPostgresDSN, err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	tenants := tenant.NewStore(conn)
	if err := tenants.Bootstrap(context.Background()); err != nil {
		t.Fatalf("Bootstrap() error: %v", err)
	}
	tn := createTestTenant(t, tenants, conn, "acme-dispatch-failedmod")

	reg := &registry.ModuleRegistry{}
	if _, err := reg.Update(map[string]*module.LoadedModule{
		"brokenmod": {
			Manifest: manifest.Manifest{Name: "brokenmod"},
			Status:   module.StatusFailed,
		},
	}); err != nil {
		t.Fatalf("registry Update: %v", err)
	}
	brokenMux := http.NewServeMux()
	RegisterActivityDispatchRoute(brokenMux, ActivityDispatchDeps{Registry: reg, Tenants: tenants})

	w := postActivityDispatch(t, brokenMux, map[string]any{
		"module":    "brokenmod",
		"activity":  "whatever",
		"tenant_id": tn.ID,
	})

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d; body = %s", w.Code, http.StatusServiceUnavailable, w.Body.String())
	}
	env := decodeEnvelope(t, w)
	if env.Error == nil || env.Error.Code != "module_unavailable" {
		t.Errorf("envelope.Error = %+v, want code module_unavailable", env.Error)
	}
}

func TestActivityDispatch_MissingFieldsIsBadRequest(t *testing.T) {
	mux, _, _ := newActivityDispatchMux(t)

	w := postActivityDispatch(t, mux, map[string]any{"tenant_id": "whatever"})

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body = %s", w.Code, http.StatusBadRequest, w.Body.String())
	}
}
