package wasm

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/abi"
	"github.com/djangbahevans/goerp/internal/engine/config"
	"github.com/djangbahevans/goerp/internal/engine/manifest"
	"github.com/vmihailenco/msgpack/v5"
)

// newSearchHostcallTestRuntime is newHostcallTestRuntime (hostcall_e2e_test.go)
// with the larger memory cap a real module linking in sdk/go/search/msgpack
// needs, same reasoning as newAuthzHostcallTestRuntime (host_authz_e2e_test.go).
func newSearchHostcallTestRuntime(t *testing.T, primaryDB *sql.DB) *Runtime {
	t.Helper()

	rt, err := New(&config.Config{
		CompilationCache:  filepath.Join(t.TempDir(), "cache"),
		Environment:       string(config.Production),
		PoolMaxMemoryByes: 8 << 20,
	}, primaryDB, nil, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = rt.Close(context.Background()) })
	return rt
}

// compileSearchCallerFixture compiles testdata/searchcallerfixture — a
// real module built on the actual sdk/go/search package — to wasip1
// WASM, the same way compileAuthzCallerFixture (host_authz_e2e_test.go)
// compiles testdata/authzcallerfixture. Proves goerp#419's acceptance
// criterion: a real compiled module can call search.Query through the
// real SDK wrapper against a real engine instance and get back real
// trigram-ranked hits.
func compileSearchCallerFixture(t *testing.T) []byte {
	t.Helper()

	wasmPath := filepath.Join(t.TempDir(), "searchcallerfixture.wasm")
	cmd := exec.Command("go", "build", "-buildmode=c-shared", "-o", wasmPath, "./testdata/searchcallerfixture")
	cmd.Env = append(os.Environ(), "GOOS=wasip1", "GOARCH=wasm")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("compile testdata/searchcallerfixture: %v\n%s", err, out)
	}

	data, err := os.ReadFile(wasmPath)
	if err != nil {
		t.Fatalf("read compiled fixture: %v", err)
	}
	return data
}

// searchQueryResult mirrors testdata/searchcallerfixture's own result
// envelope by field name and msgpack tag.
type searchQueryResult struct {
	OK        bool     `msgpack:"ok"`
	Error     string   `msgpack:"error,omitempty"`
	Names     []string `msgpack:"names,omitempty"`
	TotalHits int64    `msgpack:"total_hits,omitempty"`
}

func TestSearchCallerFixture_Query_RoundTripsThroughRealModule(t *testing.T) {
	primaryDB := openTestPrimaryDB(t)
	ctx := context.Background()

	slug := fmt.Sprintf("searche2e%d", time.Now().UnixNano())
	createFixtureTenantSchema(t, primaryDB, slug)
	newSearchFixtureTable(t, primaryDB, slug)

	modCtx := newSearchModuleContext(slug, abi.CapSearchQuery, []manifest.SearchIndex{widgetSearchIndex()})

	wasmBytes := compileSearchCallerFixture(t)
	r := newSearchHostcallTestRuntime(t, primaryDB)

	compiled, err := r.wazero.CompileModule(ctx, wasmBytes)
	if err != nil {
		t.Fatalf("CompileModule: %v", err)
	}
	t.Cleanup(func() { _ = compiled.Close(ctx) })

	inst, err := newModuleInstance(ctx, fmt.Sprintf("searchcallerfixture-%d", time.Now().UnixNano()), compiled, r.wazero)
	if err != nil {
		t.Fatalf("newModuleInstance: %v", err)
	}
	inst.SetModuleContext(modCtx)
	r.RegisterInstance(inst)
	t.Cleanup(func() { r.UnregisterInstance(inst) })

	fn := inst.module.ExportedFunction("run_search_query")
	if fn == nil {
		t.Fatal("fixture has no export run_search_query")
	}
	results, err := fn.Call(ctx)
	if err != nil {
		t.Fatalf("call run_search_query: %v", err)
	}

	packed := results[0]
	ptr := uint32(packed >> 32)
	length := uint32(packed)
	raw, ok := inst.module.Memory().Read(ptr, length)
	if !ok {
		t.Fatalf("read result at ptr=%d len=%d: out of bounds", ptr, length)
	}

	var out searchQueryResult
	if err := msgpack.Unmarshal(raw, &out); err != nil {
		t.Fatalf("unmarshal searchQueryResult: %v", err)
	}
	if !out.OK {
		t.Fatalf("run_search_query failed: %s", out.Error)
	}
	if out.TotalHits != 2 {
		t.Errorf("TotalHits = %d, want 2", out.TotalHits)
	}
	if len(out.Names) != 2 {
		t.Fatalf("len(Names) = %d, want 2, got %v", len(out.Names), out.Names)
	}
}
