package wasm

import (
	"database/sql"
	"fmt"
	"strings"
	"testing"
	"time"

	abiv1 "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/djangbahevans/goerp/internal/engine/abi"
	"github.com/djangbahevans/goerp/internal/engine/domain"
	"github.com/djangbahevans/goerp/internal/engine/manifest"
	"github.com/djangbahevans/goerp/internal/engine/permission"
	"github.com/djangbahevans/goerp/internal/engine/policy"
	"github.com/djangbahevans/goerp/internal/engine/tenantschema"
	"github.com/djangbahevans/goerp/sdk/go/model"
)

const (
	policyUserID    = "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
	policyOtherID   = "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb"
	ownInvoiceID    = "11111111-1111-1111-1111-111111111111"
	otherInvoiceID  = "22222222-2222-2222-2222-222222222222"
	voidInvoiceID   = "33333333-3333-3333-3333-333333333333"
	missingInvoice  = "99999999-9999-9999-9999-999999999999"
	invoiceReadPerm = "testmodule:invoice:read"
)

func invoiceModelDecl() model.ModelDeclaration {
	return model.ModelDeclaration{
		Name: "invoice",
		Fields: []model.NamedField{
			{Name: "id", Def: model.UUID().Required().PrimaryKey()},
			{Name: "owner_id", Def: model.UUID()},
			{Name: "state", Def: model.Text()},
		},
	}
}

// newInvoiceFixture creates a tenant schema with an invoice table of three
// rows: one owned by policyUserID, one by policyOtherID and one void invoice
// owned by policyUserID.
func newInvoiceFixture(t *testing.T) (db *sql.DB, slug string) {
	t.Helper()

	db = openTestPrimaryDB(t)
	slug = fmt.Sprintf("authzpolicy%d", time.Now().UnixNano())
	createFixtureTenantSchema(t, db, slug)

	schema := tenantschema.Name(slug)
	stmts := []string{
		fmt.Sprintf(`CREATE TABLE %s.invoice (id uuid PRIMARY KEY, owner_id uuid, state text)`, schema),
		fmt.Sprintf(`INSERT INTO %s.invoice VALUES ('%s', '%s', 'open'), ('%s', '%s', 'open'), ('%s', '%s', 'void')`,
			schema, ownInvoiceID, policyUserID, otherInvoiceID, policyOtherID, voidInvoiceID, policyUserID),
	}
	for _, stmt := range stmts {
		if _, err := db.ExecContext(t.Context(), stmt); err != nil {
			t.Fatalf("%s: %v", stmt, err)
		}
	}
	return db, slug
}

func invoicePolicies(t *testing.T, policies ...manifest.Policy) []policy.Policy {
	t.Helper()

	reg := policy.New()
	reg.Register(policies, map[string][]model.ModelDeclaration{"testmodule": {invoiceModelDecl()}})
	got := reg.For(invoiceReadPerm)
	if len(got) != len(policies) {
		t.Fatalf("registered %d policies, want %d", len(got), len(policies))
	}
	return got
}

func invoiceModuleContext(slug string) *ModuleContext {
	permReg := permission.NewPermissionRegistry()
	permReg.Register("testmodule", []manifest.Permission{{Name: invoiceReadPerm}})
	var granted permission.PermissionBitfield
	idx, _ := permReg.Index(invoiceReadPerm)
	granted.Set(idx)

	return NewModuleContext("req-1", "testmodule", policyUserID, "contact-1", []string{"sales_manager"}, granted,
		"tenant-id-1", slug, "trace-1", abi.CapAuthzCheck, nil,
		ModuleSnapshot{PermissionRegistry: permReg})
}

func TestEvaluateRecordPolicies(t *testing.T) {
	db, slug := newInvoiceFixture(t)
	mc := invoiceModuleContext(slug)

	own := manifest.Policy{Name: "testmodule:invoice:own_only", AppliesTo: invoiceReadPerm, Condition: "record.owner_id = current_user.id"}
	notVoid := manifest.Policy{Name: "testmodule:invoice:not_void", AppliesTo: invoiceReadPerm, Condition: "record.state != 'void'"}
	manager := manifest.Policy{Name: "testmodule:invoice:managers", AppliesTo: invoiceReadPerm, Condition: "user_has_role('sales_manager')"}
	otherRole := manifest.Policy{Name: "testmodule:invoice:auditors", AppliesTo: invoiceReadPerm, Condition: "user_has_role('auditor')"}
	restrictiveNotVoid := manifest.Policy{Name: "testmodule:invoice:r_not_void", AppliesTo: invoiceReadPerm, Condition: "record.state != 'void'", Combine: "AND"}
	restrictiveOwn := manifest.Policy{Name: "testmodule:invoice:r_own", AppliesTo: invoiceReadPerm, Condition: "record.owner_id = current_user.id", Combine: "AND"}

	tests := []struct {
		name         string
		policies     []manifest.Policy
		resource     string
		wantAllowed  bool
		wantInReason string
		wantErrCode  string
	}{
		{"owner passes the ownership policy", []manifest.Policy{own}, ownInvoiceID, true, "", ""},
		{"another user's record is rejected", []manifest.Policy{own}, otherInvoiceID, false, `policy "testmodule:invoice:own_only"`, ""},
		{"any admitting policy is enough (OR)", []manifest.Policy{own, manager}, otherInvoiceID, true, "", ""},
		{"no policy admitting the record", []manifest.Policy{own, otherRole}, otherInvoiceID, false, "any of the 2 permissive policies", ""},
		{"a policy hiding the record from its owner", []manifest.Policy{notVoid}, voidInvoiceID, false, "not_void", ""},
		{"a missing record", []manifest.Policy{own}, missingInvoice, false, "", abiv1.ErrCodeAuthzResourceNotFound},
		{"a malformed id", []manifest.Policy{own}, "not-a-uuid", false, "", abiv1.ErrCodeAuthzResourceNotFound},
		{"a role-only policy reads no record field", []manifest.Policy{manager}, otherInvoiceID, true, "", ""},
		{"a restrictive policy that holds", []manifest.Policy{own, restrictiveNotVoid}, ownInvoiceID, true, "", ""},
		{"a restrictive policy that rejects", []manifest.Policy{own, restrictiveNotVoid}, voidInvoiceID, false, `restrictive policy "testmodule:invoice:r_not_void"`, ""},
		{"a restrictive policy rejects what a permissive one admits", []manifest.Policy{manager, restrictiveNotVoid}, voidInvoiceID, false, "restrictive policy", ""},
		{"only restrictive policies admit nothing", []manifest.Policy{restrictiveNotVoid}, ownInvoiceID, false, "no permissive policy", ""},
		{"restrictive policies all have to hold", []manifest.Policy{manager, restrictiveNotVoid, restrictiveOwn}, otherInvoiceID, false, `"testmodule:invoice:r_own"`, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			allowed, reason, hostErr := evaluateRecordPolicies(t.Context(), db, mc, invoicePolicies(t, tc.policies...), tc.resource)

			if tc.wantErrCode != "" {
				if hostErr == nil || hostErr.Code != tc.wantErrCode {
					t.Fatalf("hostErr = %+v, want code %q", hostErr, tc.wantErrCode)
				}
				return
			}
			if hostErr != nil {
				t.Fatalf("unexpected host error: %+v", hostErr)
			}
			if allowed != tc.wantAllowed {
				t.Errorf("allowed = %v (reason %q), want %v", allowed, reason, tc.wantAllowed)
			}
			if !strings.Contains(reason, tc.wantInReason) {
				t.Errorf("reason = %q, want it to contain %q", reason, tc.wantInReason)
			}
		})
	}
}

func TestEvaluateRecordPolicies_AnUnresolvablePolicyIsNeverAnAllow(t *testing.T) {
	db, slug := newInvoiceFixture(t)
	mc := invoiceModuleContext(slug)
	reg := policy.New()
	reg.Register([]manifest.Policy{
		{Name: "testmodule:invoice:managers", AppliesTo: invoiceReadPerm, Condition: "user_has_role('sales_manager')"},
		{Name: "testmodule:invoice:broken", AppliesTo: invoiceReadPerm, Condition: "record.state ="},
	}, map[string][]model.ModelDeclaration{"testmodule": {invoiceModelDecl()}})

	allowed, _, hostErr := evaluateRecordPolicies(t.Context(), db, mc, reg.For(invoiceReadPerm), ownInvoiceID)

	if allowed || hostErr == nil || hostErr.Code != abiv1.ErrCodeAuthzPolicyEvaluation {
		t.Errorf("allowed = %v, hostErr = %+v, want a policy_evaluation_failed error and no allow", allowed, hostErr)
	}
}

func TestEvaluateRecordPolicies_AnEvaluationErrorIsNeverAnAllow(t *testing.T) {
	db, slug := newInvoiceFixture(t)
	mc := invoiceModuleContext(slug)
	broken := manifest.Policy{Name: "testmodule:invoice:broken", AppliesTo: invoiceReadPerm, Condition: "record.state > 3"}
	manager := manifest.Policy{Name: "testmodule:invoice:managers", AppliesTo: invoiceReadPerm, Condition: "user_has_role('sales_manager')"}

	allowed, _, hostErr := evaluateRecordPolicies(t.Context(), db, mc, invoicePolicies(t, manager, broken), ownInvoiceID)

	if allowed || hostErr == nil || hostErr.Code != abiv1.ErrCodeAuthzPolicyEvaluation {
		t.Errorf("allowed = %v, hostErr = %+v, want a policy_evaluation_failed error and no allow", allowed, hostErr)
	}
}

// TestEvaluateRecordPolicies_AgreesWithRLS installs the compiled form of each
// policy set on the table and requires the in-memory verdict for a record to
// equal whether the tenant role can see that record.
func TestEvaluateRecordPolicies_AgreesWithRLS(t *testing.T) {
	db, slug := newInvoiceFixture(t)
	grantFixtureTables(t, db, slug, "invoice")
	mc := invoiceModuleContext(slug)
	schema := tenantschema.Name(slug)

	if _, err := db.ExecContext(t.Context(), fmt.Sprintf(`ALTER TABLE %s.invoice ENABLE ROW LEVEL SECURITY`, schema)); err != nil {
		t.Fatalf("enable RLS: %v", err)
	}

	own := manifest.Policy{Name: "own", AppliesTo: invoiceReadPerm, Condition: "record.owner_id = current_user.id"}
	notVoid := manifest.Policy{Name: "not_void", AppliesTo: invoiceReadPerm, Condition: "record.state != 'void'"}
	manager := manifest.Policy{Name: "managers", AppliesTo: invoiceReadPerm, Condition: "user_has_role('sales_manager')"}
	auditor := manifest.Policy{Name: "auditors", AppliesTo: invoiceReadPerm, Condition: "user_has_role('auditor')"}
	openOwn := manifest.Policy{Name: "open_own", AppliesTo: invoiceReadPerm, Condition: "record.owner_id = current_user.id AND NOT record.state = 'void'"}
	rNotVoid := manifest.Policy{Name: "r_not_void", AppliesTo: invoiceReadPerm, Condition: "record.state != 'void'", Combine: "AND"}
	rAuditor := manifest.Policy{Name: "r_auditors", AppliesTo: invoiceReadPerm, Condition: "user_has_role('auditor')", Combine: "AND"}
	rOwn := manifest.Policy{Name: "r_own", AppliesTo: invoiceReadPerm, Condition: "record.owner_id = current_user.id", Combine: "AND"}

	sets := map[string][]manifest.Policy{
		"restrictive narrows a permissive one":         {notVoid, rOwn},
		"restrictive role not held":                    {manager, rAuditor},
		"restrictive only":                             {rNotVoid},
		"restrictive and a permissive that admits all": {manager, rNotVoid},
		"two restrictive policies":                     {manager, rNotVoid, rOwn},
		"permissive or, narrowed by a restrictive":     {own, auditor, rNotVoid},
		"ownership":                    {own},
		"not void":                     {notVoid},
		"role held":                    {manager},
		"role not held":                {auditor},
		"ownership or role not held":   {own, auditor},
		"ownership or not void":        {own, notVoid},
		"compound condition":           {openOwn},
		"role not held or not void":    {auditor, notVoid},
		"ownership, void and role not": {own, notVoid, auditor},
	}
	for name, set := range sets {
		t.Run(name, func(t *testing.T) {
			dropPolicies(t, db, schema)
			for _, p := range set {
				installRLSPolicy(t, db, schema, p)
			}
			policies := invoicePolicies(t, set...)

			for _, id := range []string{ownInvoiceID, otherInvoiceID, voidInvoiceID} {
				allowed, _, hostErr := evaluateRecordPolicies(t.Context(), db, mc, policies, id)
				if hostErr != nil {
					t.Fatalf("%s: %+v", id, hostErr)
				}
				if visible := visibleUnderRLS(t, db, mc, id); allowed != visible {
					t.Errorf("record %s: in memory = %v, RLS visible = %v", id, allowed, visible)
				}
			}
		})
	}
}

func dropPolicies(t *testing.T, db *sql.DB, schema string) {
	t.Helper()
	rows, err := db.QueryContext(t.Context(), `SELECT policyname FROM pg_policies WHERE schemaname = $1 AND tablename = 'invoice'`, strings.Trim(schema, `"`))
	if err != nil {
		t.Fatalf("list policies: %v", err)
	}
	var names []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			t.Fatalf("scan policy name: %v", err)
		}
		names = append(names, n)
	}
	if err := rows.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	for _, n := range names {
		if _, err := db.ExecContext(t.Context(), fmt.Sprintf(`DROP POLICY %s ON %s.invoice`, quoteIdentifier(n), schema)); err != nil {
			t.Fatalf("drop policy %s: %v", n, err)
		}
	}
}

func installRLSPolicy(t *testing.T, db *sql.DB, schema string, p manifest.Policy) {
	t.Helper()
	expr, err := domain.Parse(p.Condition)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	compiled, err := domain.CompileToRLS(expr)
	if err != nil {
		t.Fatalf("CompileToRLS: %v", err)
	}
	kind := "PERMISSIVE"
	if p.Restrictive() {
		kind = "RESTRICTIVE"
	}
	stmt := fmt.Sprintf(`CREATE POLICY %s ON %s.invoice AS %s FOR SELECT USING (%s)`, quoteIdentifier(p.Name), schema, kind, compiled)
	if _, err := db.ExecContext(t.Context(), stmt); err != nil {
		t.Fatalf("%s: %v", stmt, err)
	}
}

// visibleUnderRLS reports whether the caller, running as the tenant role with
// its session variables set, can select the invoice.
func visibleUnderRLS(t *testing.T, db *sql.DB, mc *ModuleContext, id string) bool {
	t.Helper()
	tx, err := db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatalf("BeginTx: %v", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := applyTenantScopeAsTenantRole(t.Context(), tx, mc); err != nil {
		t.Fatalf("applyTenantScopeAsTenantRole: %v", err)
	}
	var n int
	if err := tx.QueryRowContext(t.Context(), `SELECT count(*) FROM invoice WHERE id = $1`, id).Scan(&n); err != nil {
		t.Fatalf("select as tenant role: %v", err)
	}
	return n == 1
}
