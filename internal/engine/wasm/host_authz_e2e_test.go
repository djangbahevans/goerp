package wasm

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	abiv1 "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/djangbahevans/goerp/internal/engine/abi"
	"github.com/djangbahevans/goerp/internal/engine/config"
	"github.com/djangbahevans/goerp/internal/engine/fieldsec"
	"github.com/djangbahevans/goerp/internal/engine/manifest"
	"github.com/djangbahevans/goerp/internal/engine/policy"
	"github.com/djangbahevans/goerp/internal/engine/wasm/wasmtest"
	"github.com/djangbahevans/goerp/sdk/go/model"
	"github.com/djangbahevans/goerp/sdk/go/perm"
	"github.com/vmihailenco/msgpack/v5"
)

func newAuthzHostcallTestRuntime(t *testing.T) *Runtime {
	t.Helper()

	rt, err := New(&config.Config{
		CompilationCache:  wasmtest.SharedCompilationCacheDir(),
		Environment:       string(config.Production),
		PoolMaxMemoryByes: 8 << 20,
	}, nil, nil, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = rt.Close(context.Background()) })
	return rt
}

func compileAuthzCallerFixture(t *testing.T) []byte {
	t.Helper()

	wasmPath := filepath.Join(t.TempDir(), "authzcallerfixture.wasm")
	cmd := exec.CommandContext(t.Context(), "go", "build", "-buildmode=c-shared", "-o", wasmPath, "./testdata/authzcallerfixture")
	cmd.Env = append(os.Environ(), "GOOS=wasip1", "GOARCH=wasm")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("compile testdata/authzcallerfixture: %v\n%s", err, out)
	}

	data, err := os.ReadFile(wasmPath)
	if err != nil {
		t.Fatalf("read compiled fixture: %v", err)
	}

	wasmtest.Precompile(t, data, wasmtest.SharedCompilationCacheDir())

	return data
}

type authzStepResult struct {
	Step      string `msgpack:"step"`
	OK        bool   `msgpack:"ok"`
	Allowed   bool   `msgpack:"allowed"`
	Forbidden bool   `msgpack:"forbidden,omitempty"`
	Text      string `msgpack:"text,omitempty"`
	Error     string `msgpack:"error,omitempty"`
}

type authzFlowReport struct {
	Steps []authzStepResult `msgpack:"steps"`
}

func runAuthzCallerFixture(t *testing.T, modCtx *ModuleContext) authzFlowReport {
	t.Helper()
	return runAuthzCallerFixtureWithDB(t, modCtx, nil)
}

func runAuthzCallerFixtureWithDB(t *testing.T, modCtx *ModuleContext, db *sql.DB) authzFlowReport {
	t.Helper()

	ctx := t.Context()
	wasmBytes := compileAuthzCallerFixture(t)
	r := newAuthzHostcallTestRuntime(t)
	if db != nil {
		r.SetSchemaSyncDB(db)
	}

	compiled, err := r.wazero.CompileModule(ctx, wasmBytes)
	if err != nil {
		t.Fatalf("CompileModule: %v", err)
	}
	t.Cleanup(func() { _ = compiled.Close(context.WithoutCancel(ctx)) })

	inst, err := newModuleInstance(ctx, fmt.Sprintf("authzcallerfixture-%d", time.Now().UnixNano()), compiled, r)
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

func TestAuthzCallerFixture_FieldCheck(t *testing.T) {
	readPermission := perm.Ref("contacts:contact:financials_read")
	writePermission := perm.Ref("hr:employee:banking_read")

	for _, tc := range []struct {
		name        string
		permissions []string
		read        bool
		write       bool
	}{
		{name: "denied"},
		{
			name:        "read",
			permissions: []string{readPermission.Name()},
			read:        true,
		},
		{
			name:        "write",
			permissions: []string{writePermission.Name()},
			write:       true,
		},
		{
			name:        "both",
			permissions: []string{readPermission.Name(), writePermission.Name()},
			read:        true,
			write:       true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			modCtx := newFieldSecModuleContext("authze2e-"+tc.name, tc.permissions...)
			modCtx.capabilities = abi.CapAuthzCheck

			registry := fieldsec.New()
			registry.Register("widgets", []model.ModelDeclaration{
				{
					Name: "kind_matrix",
					Fields: []model.NamedField{
						{
							Name: "integer_field",
							Def: model.Integer().Access(
								model.AccessRead(readPermission),
								model.AccessWrite(writePermission),
							),
						},
						{
							Name: "boolean_field",
							Def: model.Boolean().Access(
								model.AccessRead(readPermission),
								model.AccessWrite(writePermission),
							),
						},
						{
							Name: "timestamptz_field",
							Def: model.TimestampTZ().Access(
								model.AccessRead(readPermission),
								model.AccessWrite(writePermission),
							),
						},
						{
							Name: "jsonb_field",
							Def: model.JSONB().Access(
								model.AccessRead(readPermission),
								model.AccessWrite(writePermission),
							),
						},
					},
				},
				{
					Name: "kind_probe",
					Fields: []model.NamedField{
						{
							Name: "integer_field",
							Def: model.Integer().Access(
								model.AccessRead(writePermission),
								model.AccessWrite(readPermission),
							),
						},
					},
				},
			})
			modCtx.snapshot.FieldSecRegistry = registry

			report := runAuthzCallerFixture(t, modCtx)

			for _, field := range []string{"restricted_field", "bool_field", "time_field", "bytes_field", "unrestricted_field", "other_model_field"} {
				for _, access := range []struct {
					kind              string
					allowed           bool
					otherModelAllowed bool
				}{
					{"read", tc.read, tc.write},
					{"write", tc.write, tc.read},
				} {
					want := access.allowed
					switch field {
					case "unrestricted_field":
						want = true
					case "other_model_field":
						want = access.otherModelAllowed
					}

					step := field + "_" + access.kind
					got := authzStep(t, report, step)
					if !got.OK || got.Allowed != want {
						t.Errorf("%s = %+v, want allowed=%v without error", step, got, want)
					}
				}
			}
		})
	}
}

func TestAuthzCallerFixture_CheckAndRequire_RoundTripThroughRealModule(t *testing.T) {
	modCtx := newFieldSecModuleContext("authze2e-check", "contacts:contact:financials_read")
	modCtx.capabilities = abi.CapAuthzCheck

	report := runAuthzCallerFixture(t, modCtx)

	tests := []struct {
		step      string
		allowed   bool
		wantError string
	}{
		{"check_financials", true, ""},
		{"check_banking", false, ""},
		{"check_undeclared", false, ""},
		{"require_financials", true, ""},
	}
	for _, tc := range tests {
		t.Run(tc.step, func(t *testing.T) {
			got := authzStep(t, report, tc.step)
			if tc.wantError != "" {
				if got.OK || !strings.Contains(got.Error, tc.wantError) {
					t.Fatalf("step = %+v, want an error containing %q", got, tc.wantError)
				}
				return
			}
			if !got.OK || got.Allowed != tc.allowed {
				t.Errorf("step = %+v, want OK with allowed=%v", got, tc.allowed)
			}
		})
	}

	banking := authzStep(t, report, "require_banking")
	if !banking.OK || !banking.Forbidden || !strings.Contains(banking.Error, abiv1.ErrCodeAuthzForbidden) {
		t.Errorf("require_banking = %+v, want an *authz.ForbiddenError carrying authz.forbidden", banking)
	}
}

func TestAuthzCallerFixture_CheckWithoutCapabilityIsDenied(t *testing.T) {
	modCtx := newFieldSecModuleContext("authze2e-nocap", "contacts:contact:financials_read")

	report := runAuthzCallerFixture(t, modCtx)

	for _, step := range []string{"check_financials", "require_financials", "user_roles", "restricted_field_read", "restricted_field_write", "unrestricted_field_read", "unrestricted_field_write"} {
		got := authzStep(t, report, step)
		if got.OK || !strings.Contains(got.Error, abiv1.ErrCodeCapabilityDenied) {
			t.Errorf("%s = %+v, want a capability_denied error", step, got)
		}
	}
}

func TestEvaluatePermissionCheck(t *testing.T) {
	granted := newFieldSecModuleContext("authztest-perm-granted", "contacts:contact:financials_read")

	if ok, reason := evaluatePermissionCheck(granted, "contacts:contact:financials_read"); !ok || reason != "" {
		t.Errorf("held permission = %v, %q, want allowed with no reason", ok, reason)
	}
	if ok, reason := evaluatePermissionCheck(granted, "hr:employee:banking_read"); ok || !strings.Contains(reason, "does not hold") {
		t.Errorf("declared but not held = %v, %q, want a does-not-hold reason", ok, reason)
	}
	if ok, reason := evaluatePermissionCheck(granted, "nowhere:thing:read"); ok || !strings.Contains(reason, "not declared") {
		t.Errorf("undeclared = %v, %q, want a not-declared reason", ok, reason)
	}
	if ok, _ := evaluatePermissionCheck(&ModuleContext{}, "contacts:contact:financials_read"); ok {
		t.Error("a context with no permission registry allowed a permission")
	}
}

func TestAuthzCallerFixture_RecordScopedCheck_EvaluatesPoliciesThroughRealModule(t *testing.T) {
	db, slug := newInvoiceFixture(t)
	seedRoles(t, db, slug)
	modCtx := invoiceModuleContext(slug)
	policies := policy.New()
	policies.Register([]manifest.Policy{{
		Name: "testmodule:invoice:own_only", AppliesTo: invoiceReadPerm, Condition: "record.owner_id = current_user.id",
	}}, map[string][]model.ModelDeclaration{"testmodule": {invoiceModelDecl()}})
	modCtx.snapshot.PolicyRegistry = policies

	report := runAuthzCallerFixtureWithDB(t, modCtx, db)

	if got := authzStep(t, report, "check_record_admitted"); !got.OK || !got.Allowed {
		t.Errorf("check_record_admitted = %+v, want allowed", got)
	}
	if got := authzStep(t, report, "check_record_rejected"); !got.OK || got.Allowed {
		t.Errorf("check_record_rejected = %+v, want a denial without an error", got)
	}
	if got := authzStep(t, report, "check_record_missing"); got.OK || !strings.Contains(got.Error, abiv1.ErrCodeAuthzResourceNotFound) {
		t.Errorf("check_record_missing = %+v, want authz.resource_not_found", got)
	}
	if got := authzStep(t, report, "user_roles"); !got.OK || got.Text != "admin*=36,viewer=36" {
		t.Errorf("user_roles = %+v, want admin (built in) and viewer, each with a 36-character id", got)
	}
	if got := authzStep(t, report, "row_filter"); !got.OK || got.Text != `AND (("owner_id" = $1))` {
		t.Errorf("row_filter = %+v, want the ownership policy as a fragment", got)
	}
	if got := authzStep(t, report, "require_record_rejected"); !got.OK || !got.Forbidden {
		t.Errorf("require_record_rejected = %+v, want an *authz.ForbiddenError", got)
	}
}
