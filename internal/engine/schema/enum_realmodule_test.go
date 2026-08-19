package schema

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/config"
	"github.com/djangbahevans/goerp/internal/engine/loader"
	"github.com/djangbahevans/goerp/internal/engine/module"
	"github.com/djangbahevans/goerp/internal/engine/wasm"
)

// compileEnumFixture compiles testdata/enumfixture — a real module
// declaring an Enum-kind field and its matching model.EnumType via the
// actual SDK — to wasip1 WASM, the same way goerp#234's realfixture is
// compiled (see that fixture's own doc comment for why -buildmode=c-shared
// is required on wasip1).
func compileEnumFixture(t *testing.T) []byte {
	t.Helper()

	wasmPath := filepath.Join(t.TempDir(), "enumfixture.wasm")
	cmd := exec.Command("go", "build", "-buildmode=c-shared", "-o", wasmPath, "./testdata/enumfixture")
	cmd.Env = append(os.Environ(), "GOOS=wasip1", "GOARCH=wasm")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("compile testdata/enumfixture: %v\n%s", err, out)
	}

	data, err := os.ReadFile(wasmPath)
	if err != nil {
		t.Fatalf("read compiled fixture: %v", err)
	}
	return data
}

// enumFixtureManifest builds a minimal valid manifest (manifest-spec.md
// §2's required root fields) whose checksum matches wasmBytes, matching
// loader_test.go's own manifestJSON helper — duplicated here rather than
// exported from that package, since this is the only place outside
// internal/engine/loader that needs it.
func enumFixtureManifest(t *testing.T, wasmBytes []byte) []byte {
	t.Helper()
	sum := sha256.Sum256(wasmBytes)

	fields := map[string]any{
		"name":         "sales",
		"display_name": "sales",
		"type":         "domain",
		"version":      "1.0.0",
		"description":  "goerp#199 enum fixture",
		"abi_version":  "1",
		"engine":       ">=0.5.0 <1.0.0",
		"depends_on":   []string{},
		"capabilities": []string{},
		"schema": map[string]any{
			"owned_models": []string{"sales.order"},
		},
		"checksum": fmt.Sprintf("sha256:%x", sum),
	}

	data, err := json.Marshal(fields)
	if err != nil {
		t.Fatalf("marshal manifest fixture: %v", err)
	}
	return data
}

// newEnumFixtureRuntime is newTestRuntime with a larger pool memory limit
// — a real Go-compiled wasip1 binary's minimum linear memory (about 2 MiB)
// is well past what this repo's hand-assembled bytecode fixtures need,
// same reasoning as goerp#234's newRealFixtureRuntime.
func newEnumFixtureRuntime(t *testing.T) *wasm.Runtime {
	t.Helper()
	rt, err := wasm.New(&config.Config{
		CompilationCache:  filepath.Join(t.TempDir(), "cache"),
		PoolMaxMemoryByes: 64 << 20,
		Environment:       string(config.Production),
	}, nil, nil)
	if err != nil {
		t.Fatalf("wasm.New: %v", err)
	}
	t.Cleanup(func() { _ = rt.Close(context.Background()) })
	return rt
}

// TestDiffAndExecute_RealCompiledModule_EnumFieldRoundTrips is the
// goerp#199 acceptance test: a real compiled module's own
// get_model_declarations() export — not a hand-built model.ModelDeclaration/
// TypeDeclaration the way TestDiffAndExecute_CreatesEnumTypeAndColumnTogether
// uses — feeds the same Diff/Execute path, proving the SDK's actual
// wire-format output for an Enum-kind field and its EnumType survives the
// full loader.LoadModule decode and still diffs to correct DDL.
func TestDiffAndExecute_RealCompiledModule_EnumFieldRoundTrips(t *testing.T) {
	wasmBytes := compileEnumFixture(t)
	rt := newEnumFixtureRuntime(t)

	src := loader.Source{
		Name:          "sales",
		ManifestBytes: enumFixtureManifest(t, wasmBytes),
		WasmBytes:     wasmBytes,
	}
	poolCfg := wasm.PoolConfig{MaxSize: 1, WarmSize: 0, BorrowTimeout: time.Second}

	m := loader.LoadModule(context.Background(), rt, poolCfg, src)
	if m.Status == module.StatusFailed {
		t.Fatalf("LoadModule() failed: %s", m.FailureReason)
	}
	// Registered after newEnumFixtureRuntime's rt.Close cleanup, so LIFO
	// ordering drains the pool's background replenishLoop before the
	// runtime closes — otherwise the two race (goerp#234's realfixture
	// test hit this exact race in CI).
	t.Cleanup(func() { m.Pool.DrainAndClose(context.Background(), 5*time.Second) })
	if len(m.ModelDecls) != 1 || len(m.TypeDecls) != 1 {
		t.Fatalf("LoadModule() returned %d model decls, %d type decls, want 1 and 1", len(m.ModelDecls), len(m.TypeDecls))
	}

	sess, engine := setupTenantSchema(t, "difftest_enum_real")
	conn, _ := openTestPool(t, 5*time.Second)

	changes, err := engine.Diff(context.Background(), sess, m.ModelDecls, m.TypeDecls)
	if err != nil {
		t.Fatalf("Diff() error: %v", err)
	}
	if len(changes) == 0 {
		t.Fatal("Diff() on an empty schema returned no changes, want at least AddTable + the enum type")
	}

	blocked, err := engine.Execute(context.Background(), sess, m.ModelDecls, changes)
	if err != nil {
		t.Fatalf("Execute() error: %v", err)
	}
	if len(blocked) != 0 {
		t.Errorf("Execute() blocked = %v, want none", blocked)
	}

	if !tableExists(t, conn, "tenant_difftest_enum_real", "sales_orders") {
		t.Error("sales_orders table was not created")
	}
	if !columnExists(t, conn, "tenant_difftest_enum_real", "sales_orders", "state") {
		t.Error("state column was not created")
	}
	if !enumTypeExists(t, conn, "tenant_difftest_enum_real", "order_state_enum") {
		t.Error("order_state_enum type was not created")
	}
}
