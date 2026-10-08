package wasm

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	abiv1 "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/djangbahevans/goerp/internal/engine/abi"
	"github.com/djangbahevans/goerp/sdk/go/model"
	"github.com/vmihailenco/msgpack/v5"
)

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

// The round trip includes unlink to exercise buffer lifetime across reentrant host
// allocation. Module buffers must remain referenced until Deallocate so GC cannot reuse
// their addresses.
func TestOrmCallerFixture_AllFunctions_RoundTripThroughRealModule(t *testing.T) {
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
		"count":           "3",
		"sum":             "800",
		"min":             "200",
		"max":             "300",
		"avg":             "266.67",
		"mutate":          "250",
		"mutate_guard":    "",
		"unlink":          "1",
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
	if len(report.Steps) != 17 {
		t.Errorf("got %d steps, want 17 (one per host.orm.* function the fixture calls, plus a failing-guard mutate): %+v", len(report.Steps), report.Steps)
	}
}

func TestOrmCallerFixture_TxVariants_RoundTripThroughRealModule(t *testing.T) {
	primaryDB := openTestPrimaryDB(t)
	ctx := context.Background()
	wasmBytes := compileOrmCallerFixture(t)

	slug := fmt.Sprintf("ormcallerfixturetx%d", time.Now().UnixNano())
	createFixtureTenantSchema(t, primaryDB, slug)
	createFixtureWidgetsTable(t, primaryDB, slug, nil)

	mc := NewModuleContext("req-1", "testmodule", "user-1", "contact-1", []string{"admin"}, nil, "tenant-id-1", slug, "trace-1", abi.CapDBRead|abi.CapDBWrite, nil, ModuleSnapshot{ModelDecls: []model.ModelDeclaration{widgetModelDecl()}})

	r := newHostcallTestRuntime(t, primaryDB, 10)

	compiled, err := r.wazero.CompileModule(ctx, wasmBytes)
	if err != nil {
		t.Fatalf("CompileModule: %v", err)
	}
	t.Cleanup(func() { _ = compiled.Close(ctx) })

	inst, err := newModuleInstance(ctx, fmt.Sprintf("ormcallerfixturetx-%d", time.Now().UnixNano()), compiled, r.wazero)
	if err != nil {
		t.Fatalf("newModuleInstance: %v", err)
	}
	inst.SetModuleContext(mc)
	r.RegisterInstance(inst)
	t.Cleanup(func() { r.UnregisterInstance(inst) })

	fn := inst.module.ExportedFunction("run_orm_tx_flow")
	if fn == nil {
		t.Fatal("fixture has no export run_orm_tx_flow")
	}
	results, err := fn.Call(ctx)
	if err != nil {
		t.Fatalf("call run_orm_tx_flow: %v", err)
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

	wantDetail := map[string]string{
		"create_tx":          "Tx Widget A",
		"read_one_tx":        "Tx Widget A",
		"write_tx":           "",
		"create_batch_tx":    "1",
		"count_tx":           "1", // Query.Tx(tx).Count(), run before WriteTx renamed anything else matching
		"write_many_tx":      "1",
		"write_where_tx":     "1",
		"mutate_tx":          "65",
		"unlink_tx":          "1",
		"first_or_create_tx": "false", // hits the row create_tx already inserted on this same transaction
		"with_tx":            "1",     // reuses count_tx's own in-tx count value, recorded after WithTx returns
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
	if len(report.Steps) != 11 {
		t.Errorf("got %d steps, want 11: %+v", len(report.Steps), report.Steps)
	}

	// The whole point of _Tx: only WithTx's own commit persists anything.
	// A fresh, separate ORMSearch (auto-committed, not the fixture's own
	// transaction) must see exactly the rows that survived the fixture's
	// writes/unlink — proving every intermediate orm.*Tx call itself
	// committed nothing.
	out, hostErr := ORMSearch(ctx, primaryDB, mc, abiv1.ORMSearchInput{Model: "testmodule.widget"})
	if hostErr != nil {
		t.Fatalf("post-commit search failed: %+v", hostErr)
	}
	if out.Count != 1 {
		t.Errorf("post-commit widget count = %d, want 1 (Tx Widget A only — Tx Widget B was unlinked inside the same transaction)", out.Count)
	}
}
