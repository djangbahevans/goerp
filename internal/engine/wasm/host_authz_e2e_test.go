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
	"github.com/vmihailenco/msgpack/v5"
)

// newAuthzHostcallTestRuntime is newHostcallTestRuntime (hostcall_e2e_test.go)
// with no primary DB — host.authz.field_check never touches it.
func newAuthzHostcallTestRuntime(t *testing.T) *Runtime {
	t.Helper()

	rt, err := New(&config.Config{
		CompilationCache:  filepath.Join(t.TempDir(), "cache"),
		Environment:       string(config.Production),
		PoolMaxMemoryByes: 8 << 20,
	}, nil, nil, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = rt.Close(context.Background()) })
	return rt
}

// compileAuthzCallerFixture compiles testdata/authzcallerfixture — a
// real module built on the actual sdk/go/authz package, not
// hand-assembled bytecode — to wasip1 WASM, the same way
// compileStorageCallerFixture (hostcall_storage_e2e_test.go) compiles
// testdata/storagecallerfixture. Proves goerp#418's acceptance
// criterion: a real compiled module can call authz.FieldCheck through
// the real SDK wrapper against a real engine instance and get back the
// decoded allowed/denied result.
func compileAuthzCallerFixture(t *testing.T) []byte {
	t.Helper()

	wasmPath := filepath.Join(t.TempDir(), "authzcallerfixture.wasm")
	cmd := exec.Command("go", "build", "-buildmode=c-shared", "-o", wasmPath, "./testdata/authzcallerfixture")
	cmd.Env = append(os.Environ(), "GOOS=wasip1", "GOARCH=wasm")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("compile testdata/authzcallerfixture: %v\n%s", err, out)
	}

	data, err := os.ReadFile(wasmPath)
	if err != nil {
		t.Fatalf("read compiled fixture: %v", err)
	}
	return data
}

// authzStepResult mirrors testdata/authzcallerfixture's own step
// envelope by field name and msgpack tag.
type authzStepResult struct {
	Step    string `msgpack:"step"`
	OK      bool   `msgpack:"ok"`
	Allowed bool   `msgpack:"allowed"`
	Error   string `msgpack:"error,omitempty"`
}

type authzFlowReport struct {
	Steps []authzStepResult `msgpack:"steps"`
}

func runAuthzCallerFixture(t *testing.T, modCtx *ModuleContext) authzFlowReport {
	t.Helper()

	ctx := context.Background()
	wasmBytes := compileAuthzCallerFixture(t)
	r := newAuthzHostcallTestRuntime(t)

	compiled, err := r.wazero.CompileModule(ctx, wasmBytes)
	if err != nil {
		t.Fatalf("CompileModule: %v", err)
	}
	t.Cleanup(func() { _ = compiled.Close(ctx) })

	inst, err := newModuleInstance(ctx, fmt.Sprintf("authzcallerfixture-%d", time.Now().UnixNano()), compiled, r.wazero)
	if err != nil {
		t.Fatalf("newModuleInstance: %v", err)
	}
	inst.SetModuleContext(modCtx)
	r.RegisterInstance(inst)
	t.Cleanup(func() { r.UnregisterInstance(inst) })

	fn := inst.module.ExportedFunction("run_authz_flow")
	if fn == nil {
		t.Fatal("fixture has no export run_authz_flow")
	}
	results, err := fn.Call(ctx)
	if err != nil {
		t.Fatalf("call run_authz_flow: %v", err)
	}

	packed := results[0]
	ptr := uint32(packed >> 32)
	length := uint32(packed)
	raw, ok := inst.module.Memory().Read(ptr, length)
	if !ok {
		t.Fatalf("read result at ptr=%d len=%d: out of bounds", ptr, length)
	}

	var report authzFlowReport
	if err := msgpack.Unmarshal(raw, &report); err != nil {
		t.Fatalf("unmarshal authzFlowReport: %v", err)
	}
	return report
}

func authzStep(t *testing.T, report authzFlowReport, step string) authzStepResult {
	t.Helper()
	for _, s := range report.Steps {
		if s.Step == step {
			return s
		}
	}
	t.Fatalf("no step %q in report %+v", step, report)
	return authzStepResult{}
}

func TestAuthzCallerFixture_FieldCheck_RoundTripsThroughRealModule(t *testing.T) {
	// newFieldSecModuleContext (host_orm_field_security_test.go) grants
	// "user-1" (authzcallerfixture's hardcoded caller) no permissions —
	// credit_limit's ReadPermission ("contacts:contact:financials_read")
	// is denied, while unrestricted "name" is allowed.
	modCtx := newFieldSecModuleContext("authze2e-denied")
	modCtx.capabilities = abi.CapAuthzCheck

	report := runAuthzCallerFixture(t, modCtx)

	restricted := authzStep(t, report, "restricted_field_read")
	if !restricted.OK {
		t.Fatalf("restricted_field_read failed: %s", restricted.Error)
	}
	if restricted.Allowed {
		t.Error("expected credit_limit read to be denied for a caller with no financials_read permission")
	}

	unrestricted := authzStep(t, report, "unrestricted_field_read")
	if !unrestricted.OK {
		t.Fatalf("unrestricted_field_read failed: %s", unrestricted.Error)
	}
	if !unrestricted.Allowed {
		t.Error("expected an unrestricted field to always be allowed")
	}
}

func TestAuthzCallerFixture_FieldCheck_GrantedPermissionAllows(t *testing.T) {
	modCtx := newFieldSecModuleContext("authze2e-granted", "contacts:contact:financials_read")
	modCtx.capabilities = abi.CapAuthzCheck

	report := runAuthzCallerFixture(t, modCtx)

	restricted := authzStep(t, report, "restricted_field_read")
	if !restricted.OK {
		t.Fatalf("restricted_field_read failed: %s", restricted.Error)
	}
	if !restricted.Allowed {
		t.Error("expected credit_limit read to be allowed for a caller with financials_read permission")
	}
}
