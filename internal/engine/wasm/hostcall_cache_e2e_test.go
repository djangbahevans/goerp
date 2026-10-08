package wasm

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/abi"
	"github.com/djangbahevans/goerp/internal/engine/config"
	"github.com/djangbahevans/goerp/internal/engine/wasm/wasmtest"
	"github.com/vmihailenco/msgpack/v5"
)

func compileCacheCallerFixture(t *testing.T) []byte {
	t.Helper()

	wasmPath := filepath.Join(t.TempDir(), "cachecallerfixture.wasm")
	cmd := exec.Command("go", "build", "-buildmode=c-shared", "-o", wasmPath, "./testdata/cachecallerfixture")
	cmd.Env = append(os.Environ(), "GOOS=wasip1", "GOARCH=wasm")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("compile testdata/cachecallerfixture: %v\n%s", err, out)
	}

	data, err := os.ReadFile(wasmPath)
	if err != nil {
		t.Fatalf("read compiled fixture: %v", err)
	}
	return data
}

// cacheCallerResult mirrors testdata/cachecallerfixture's result envelope.
type cacheCallerResult struct {
	OK       bool   `msgpack:"ok"`
	Error    string `msgpack:"error,omitempty"`
	Found    bool   `msgpack:"found,omitempty"`
	LoadedAt int64  `msgpack:"loaded_at,omitempty"`
}

// TestCacheCallerFixture_LoadingCache_RoundTripsThroughRealModule drives a real
// compiled module's sdk/go/cache definition against the real host.cache: Get
// loads once through handle_cache_loader on a separate pooled instance and a
// second Get hits, Lookup sees the stored entry, and InvalidateAll clears it.
func TestCacheCallerFixture_LoadingCache_RoundTripsThroughRealModule(t *testing.T) {
	ctx := t.Context()
	cacheClient := openTestCacheClient(t)
	rt, err := New(&config.Config{
		CompilationCache:  wasmtest.SharedCompilationCacheDir(),
		Environment:       string(config.Production),
		PoolMaxMemoryByes: 8 << 20,
	}, openTestPrimaryDB(t), nil, cacheClient)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = rt.Close(context.Background()) })

	compiled, err := rt.wazero.CompileModule(ctx, compileCacheCallerFixture(t))
	if err != nil {
		t.Fatalf("CompileModule: %v", err)
	}
	t.Cleanup(func() { _ = compiled.Close(context.Background()) })
	pool := rt.NewPool("testmodule", compiled, PoolConfig{})
	t.Cleanup(func() { pool.DrainAndClose(context.Background(), 5*time.Second) })

	mc := NewModuleContext("req-1", "testmodule", "user-1", "", nil, nil, cacheTestTenantID(t), "tenant-slug", "trace-1",
		abi.CapCacheRead|abi.CapCacheWrite, nil, ModuleSnapshot{
			ComputeTargets: map[string]ComputeTarget{"testmodule": {Pool: pool}},
		})
	cleanupCacheNamespace(t, cacheClient, mc)

	caller, err := newModuleInstance(ctx, fmt.Sprintf("cachecallerfixture-%d", time.Now().UnixNano()), compiled, rt)
	if err != nil {
		t.Fatalf("newModuleInstance: %v", err)
	}
	caller.SetModuleContext(mc)
	rt.RegisterInstance(caller)
	t.Cleanup(func() { rt.UnregisterInstance(caller) })

	call := func(export string) cacheCallerResult {
		t.Helper()
		fn := caller.module.ExportedFunction(export)
		if fn == nil {
			t.Fatalf("fixture has no export %s", export)
		}
		results, err := fn.Call(ctx)
		if err != nil {
			t.Fatalf("call %s: %v", export, err)
		}
		raw, ok := caller.module.Memory().Read(uint32(results[0]>>32), uint32(results[0]))
		if !ok {
			t.Fatalf("read %s result: out of bounds", export)
		}
		var out cacheCallerResult
		if err := msgpack.Unmarshal(raw, &out); err != nil {
			t.Fatalf("unmarshal %s result: %v", export, err)
		}
		if !out.OK {
			t.Fatalf("%s failed: %s", export, out.Error)
		}
		return out
	}

	if call("run_lookup").Found {
		t.Fatal("Lookup hit before anything was loaded")
	}

	first := call("run_get")
	if first.LoadedAt == 0 {
		t.Fatal("first Get returned no loaded value")
	}
	if second := call("run_get"); second.LoadedAt != first.LoadedAt {
		t.Errorf("second Get loaded_at = %d, want the stored %d (loader must not rerun)", second.LoadedAt, first.LoadedAt)
	}
	if looked := call("run_lookup"); !looked.Found || looked.LoadedAt != first.LoadedAt {
		t.Errorf("Lookup = %+v, want the stored entry", looked)
	}

	call("run_invalidate_all")
	if call("run_lookup").Found {
		t.Error("Lookup hit after InvalidateAll")
	}
}
