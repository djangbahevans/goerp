package jobdispatch

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/jobqueue"
	"github.com/djangbahevans/goerp/internal/engine/manifest"
	"github.com/djangbahevans/goerp/internal/engine/module"
	"github.com/djangbahevans/goerp/internal/engine/registry"
	"github.com/djangbahevans/goerp/internal/engine/wasm"
	"github.com/vmihailenco/msgpack/v5"
)

const providerTestModuleName = "connector_paystack"

var (
	providerFixtureOnce  sync.Once
	providerFixtureBytes []byte
	providerFixtureErr   error
)

// compileProviderFixture compiles testdata/providerfixture once per test
// binary, the same way compileMigrationFixture compiles
// testdata/migrationfixture.
func compileProviderFixture(t *testing.T) []byte {
	t.Helper()
	providerFixtureOnce.Do(func() {
		dir, err := os.MkdirTemp("", "providerfixture")
		if err != nil {
			providerFixtureErr = err
			return
		}
		defer os.RemoveAll(dir)

		wasmPath := filepath.Join(dir, "providerfixture.wasm")
		cmd := exec.Command("go", "build", "-buildmode=c-shared", "-o", wasmPath, "./testdata/providerfixture")
		cmd.Env = append(os.Environ(), "GOOS=wasip1", "GOARCH=wasm")
		if out, err := cmd.CombinedOutput(); err != nil {
			providerFixtureErr = errors.New(string(out))
			return
		}
		providerFixtureBytes, providerFixtureErr = os.ReadFile(wasmPath)
	})
	if providerFixtureErr != nil {
		t.Fatalf("compile testdata/providerfixture: %v", providerFixtureErr)
	}
	return providerFixtureBytes
}

// newProviderRegistry loads the compiled provider fixture as
// providerTestModuleName, declaring provides.payment_provider when provides
// is set and no job_types either way — provider jobs are never declared.
func newProviderRegistry(t *testing.T, rt *wasm.Runtime, provides bool) *registry.ModuleRegistry {
	t.Helper()
	compiled, err := rt.CompileModule(t.Context(), compileProviderFixture(t))
	if err != nil {
		t.Fatalf("CompileModule: %v", err)
	}
	t.Cleanup(func() { _ = compiled.Close(context.Background()) })

	pool := rt.NewPool(providerTestModuleName, compiled, wasm.PoolConfig{MaxSize: 2, BorrowTimeout: 5 * time.Second})
	t.Cleanup(func() { pool.DrainAndClose(context.Background(), 5*time.Second) })

	m := manifest.Manifest{Name: providerTestModuleName, Type: "connector"}
	if provides {
		m.Provides = map[string]bool{"payment_provider": true}
	}
	reg := &registry.ModuleRegistry{}
	if _, err := reg.Update(map[string]*module.LoadedModule{
		providerTestModuleName: {Status: module.StatusReady, Pool: pool, Manifest: m},
	}); err != nil {
		t.Fatalf("ModuleRegistry.Update: %v", err)
	}
	return reg
}

func providerPayload(t *testing.T, mode string) []byte {
	t.Helper()
	data, err := msgpack.Marshal(map[string]any{"mode": mode, "reference": "ref-1"})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	return data
}

func dispatchProviderSync(t *testing.T, ctx context.Context, d *SyncDispatcher, moduleName, mode string) (int32, []byte, error) {
	t.Helper()
	return d.DispatchJobSync(ctx, wasm.SyncJobRequest{
		ModuleName: moduleName, JobType: "payment_charge", Payload: providerPayload(t, mode),
		TenantID: "tenant-1", TenantSlug: "providertest", TraceID: "trace-1",
	})
}

func TestSyncDispatcher_RealCompiledFixture_ReturnsSetResult(t *testing.T) {
	rt := newTestWasmRuntime(t)
	d := &SyncDispatcher{ModuleRegistry: newProviderRegistry(t, rt, true), Runtime: rt}

	status, result, err := dispatchProviderSync(t, t.Context(), d, providerTestModuleName, "result")
	if err != nil || status != 0 {
		t.Fatalf("DispatchJobSync = %d, %v; want 0, nil", status, err)
	}
	var got struct {
		CheckoutURL string `msgpack:"checkout_url"`
	}
	if err := msgpack.Unmarshal(result, &got); err != nil {
		t.Fatalf("decode result %x: %v", result, err)
	}
	if got.CheckoutURL != "https://checkout.example/ref-1" {
		t.Errorf("checkout_url = %q", got.CheckoutURL)
	}
}

func TestSyncDispatcher_RealCompiledFixture_NoResultAndFailure(t *testing.T) {
	rt := newTestWasmRuntime(t)
	d := &SyncDispatcher{ModuleRegistry: newProviderRegistry(t, rt, true), Runtime: rt}

	status, result, err := dispatchProviderSync(t, t.Context(), d, providerTestModuleName, "noop")
	if err != nil || status != 0 || result != nil {
		t.Errorf("noop: DispatchJobSync = %d, %x, %v; want 0, nil, nil", status, result, err)
	}

	status, _, err = dispatchProviderSync(t, t.Context(), d, providerTestModuleName, "fail")
	if err != nil || status != 1 {
		t.Errorf("fail: DispatchJobSync = %d, %v; want status 1", status, err)
	}
}

func TestSyncDispatcher_RealCompiledFixture_HangingHandlerStopsAtDeadline(t *testing.T) {
	rt := newTestWasmRuntime(t)
	d := &SyncDispatcher{ModuleRegistry: newProviderRegistry(t, rt, true), Runtime: rt}

	ctx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
	defer cancel()
	start := time.Now()
	if _, _, err := dispatchProviderSync(t, ctx, d, providerTestModuleName, "hang"); err == nil {
		t.Fatal("DispatchJobSync returned nil error for a handler that never returns")
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Errorf("DispatchJobSync took %s, want it stopped at the deadline", elapsed)
	}
}

func TestSyncDispatcher_UnknownModule(t *testing.T) {
	rt := newTestWasmRuntime(t)
	d := &SyncDispatcher{ModuleRegistry: newProviderRegistry(t, rt, true), Runtime: rt}

	_, _, err := dispatchProviderSync(t, t.Context(), d, "connector_missing", "result")
	if !errors.Is(err, wasm.ErrSyncJobTargetUnavailable) || !strings.Contains(err.Error(), "not ready") {
		t.Fatalf("err = %v, want ErrSyncJobTargetUnavailable for a module that is not ready", err)
	}
}

// A provider job isn't in JobRegistry; Work accepts it as long as the
// resolved module still provides the category, and SetResult is a no-op
// on this async path.
func TestWork_ProviderJob_RunsOnResolvedModule(t *testing.T) {
	conn, tenantStore := newTestTenantStore(t)
	tt := newFixtureTenant(t, conn, tenantStore)
	rt := newTestWasmRuntime(t)
	w := &Worker{ModuleRegistry: newProviderRegistry(t, rt, true), Runtime: rt, TenantStore: tenantStore}

	if err := runWork(t, w, jobqueue.WASMJobArgs{
		ModuleName: providerTestModuleName, JobType: "payment_charge", ProviderCategory: "payment_provider",
		TenantID: tt.ID, Payload: providerPayload(t, "result"),
	}); err != nil {
		t.Fatalf("Work: %v", err)
	}

	if err := runWork(t, w, jobqueue.WASMJobArgs{
		ModuleName: providerTestModuleName, JobType: "payment_charge", ProviderCategory: "payment_provider",
		TenantID: tt.ID, Payload: providerPayload(t, "fail"),
	}); err == nil {
		t.Fatal("Work returned nil for a failing handler, want an error so River retries")
	}
}

func TestWork_ProviderJob_ModuleNoLongerProvidesCategory(t *testing.T) {
	conn, tenantStore := newTestTenantStore(t)
	tt := newFixtureTenant(t, conn, tenantStore)
	rt := newTestWasmRuntime(t)
	w := &Worker{ModuleRegistry: newProviderRegistry(t, rt, false), Runtime: rt, TenantStore: tenantStore}

	err := runWork(t, w, jobqueue.WASMJobArgs{
		ModuleName: providerTestModuleName, JobType: "payment_charge", ProviderCategory: "payment_provider",
		TenantID: tt.ID, Payload: providerPayload(t, "result"),
	})
	if err == nil || !strings.Contains(err.Error(), "no longer provides payment_provider") {
		t.Fatalf("err = %v, want no longer provides", err)
	}
}

// Without ProviderCategory, a job type no module declares is still
// refused: the provider path doesn't loosen the ordinary ownership check.
func TestWork_UndeclaredJobTypeWithoutProviderCategoryStillRefused(t *testing.T) {
	conn, tenantStore := newTestTenantStore(t)
	tt := newFixtureTenant(t, conn, tenantStore)
	rt := newTestWasmRuntime(t)
	w := &Worker{ModuleRegistry: newProviderRegistry(t, rt, true), Runtime: rt, TenantStore: tenantStore}

	err := runWork(t, w, jobqueue.WASMJobArgs{
		ModuleName: providerTestModuleName, JobType: "payment_charge", TenantID: tt.ID, Payload: providerPayload(t, "result"),
	})
	if err == nil || !strings.Contains(err.Error(), "is not registered to module") {
		t.Fatalf("err = %v, want not registered", err)
	}
}
