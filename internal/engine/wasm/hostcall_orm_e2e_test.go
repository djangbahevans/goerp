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
	"github.com/djangbahevans/goerp/sdk/go/model"
	"github.com/vmihailenco/msgpack/v5"
)

// compileOrmCallerFixture compiles testdata/ormcallerfixture — a real
// module built on the actual sdk/go/orm package's 10 host.orm.*
// callers, not hand-assembled bytecode — to wasip1 WASM, the same way
// compileHostcallFixture (hostcall_e2e_test.go) compiles
// testdata/hostcallfixture. Proves goerp#433's acceptance criterion: a
// real compiled module can call every host.orm.* wrapper against a real
// engine instance and get back correctly-decoded results.
func compileOrmCallerFixture(t *testing.T) []byte {
	t.Helper()

	wasmPath := filepath.Join(t.TempDir(), "ormcallerfixture.wasm")
	cmd := exec.Command("go", "build", "-buildmode=c-shared", "-o", wasmPath, "./testdata/ormcallerfixture")
	cmd.Env = append(os.Environ(), "GOOS=wasip1", "GOARCH=wasm")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("compile testdata/ormcallerfixture: %v\n%s", err, out)
	}

	data, err := os.ReadFile(wasmPath)
	if err != nil {
		t.Fatalf("read compiled fixture: %v", err)
	}
	return data
}

// ormStepResult/ormFlowReport mirror testdata/ormcallerfixture's own
// result envelope by field name and msgpack tag.
type ormStepResult struct {
	Step   string `msgpack:"step"`
	OK     bool   `msgpack:"ok"`
	Error  string `msgpack:"error,omitempty"`
	Detail string `msgpack:"detail,omitempty"`
}

type ormFlowReport struct {
	Steps []ormStepResult `msgpack:"steps"`
}

// TestOrmCallerFixture_AllTenFunctions_RoundTripThroughRealModule is
// goerp#433's acceptance criterion: a real compiled module calls each
// of the 10 host.orm.* wrappers against a real engine instance and gets
// back correctly-decoded results.
//
// Also the regression test for a real bug this ticket surfaced in
// sdk/go/internal/wasmmem's real (wasip1) Allocate: its returned buffer
// had no live Go reference anywhere once Allocate returned — only a
// bare uint32 survived — so the garbage collector could (and, for
// host.orm.unlink specifically, reliably did) reclaim and reuse that
// address for the response buffer the host allocates via a reentrant
// call back into the module mid-round-trip, corrupting whichever value
// got read back afterward. Fixed by having Allocate retain the buffer
// in a package-level map until Deallocate removes it — see
// sdk/go/internal/wasmmem/mem_wasip1.go's own comment for the full
// story. Without that fix, this test's "unlink" step decodes
// Deleted:false even though the engine computed and sent true.
func TestOrmCallerFixture_AllTenFunctions_RoundTripThroughRealModule(t *testing.T) {
	primaryDB := openTestPrimaryDB(t)
	ctx := context.Background()
	wasmBytes := compileOrmCallerFixture(t)

	slug := fmt.Sprintf("ormcallerfixture%d", time.Now().UnixNano())
	createFixtureTenantSchema(t, primaryDB, slug)
	createFixtureWidgetsTable(t, primaryDB, slug, nil)

	mc := NewModuleContext("req-1", "testmodule", "user-1", "contact-1", []string{"admin"}, nil, "tenant-id-1", slug, "trace-1", abi.CapDBRead|abi.CapDBWrite, nil, ModuleSnapshot{ModelDecls: []model.ModelDeclaration{widgetModelDecl()}})

	r := newHostcallTestRuntime(t, primaryDB, 10)

	compiled, err := r.wazero.CompileModule(ctx, wasmBytes)
	if err != nil {
		t.Fatalf("CompileModule: %v", err)
	}
	t.Cleanup(func() { _ = compiled.Close(ctx) })

	inst, err := newModuleInstance(ctx, fmt.Sprintf("ormcallerfixture-%d", time.Now().UnixNano()), compiled, r.wazero)
	if err != nil {
		t.Fatalf("newModuleInstance: %v", err)
	}
	inst.SetModuleContext(mc)
	r.RegisterInstance(inst)
	t.Cleanup(func() { r.UnregisterInstance(inst) })

	fn := inst.module.ExportedFunction("run_orm_flow")
	if fn == nil {
		t.Fatal("fixture has no export run_orm_flow")
	}
	results, err := fn.Call(ctx)
	if err != nil {
		t.Fatalf("call run_orm_flow: %v", err)
	}

	packed := results[0]
	ptr := uint32(packed >> 32)
	length := uint32(packed)
	raw, ok := inst.module.Memory().Read(ptr, length)
	if !ok {
		t.Fatalf("read result at ptr=%d len=%d: out of bounds", ptr, length)
	}

	var report ormFlowReport
	if err := msgpack.Unmarshal(raw, &report); err != nil {
		t.Fatalf("unmarshal ormFlowReport: %v", err)
	}

	// Every step must have succeeded (a decode failure or HostError
	// would show up here) — and each step's Detail proves the response
	// was actually decoded correctly, not just that the call didn't
	// error.
	wantDetail := map[string]string{
		"create":          "Widget A",
		"read":            "1",
		"search":          "1",
		"search_read":     "1",
		"create_batch":    "2",
		"first_or_create": "false", // "Widget A" already exists from the create step
		"write_many":      "2",
		"write_where":     "2",
		"unlink":          "true",
	}
	for _, s := range report.Steps {
		if !s.OK {
			t.Errorf("step %q failed: %s", s.Step, s.Error)
			continue
		}
		if want, ok := wantDetail[s.Step]; ok && s.Detail != want {
			t.Errorf("step %q detail = %q, want %q", s.Step, s.Detail, want)
		}
	}
	if len(report.Steps) != 10 {
		t.Errorf("got %d steps, want 10 (one per host.orm.* function the fixture calls): %+v", len(report.Steps), report.Steps)
	}
}
