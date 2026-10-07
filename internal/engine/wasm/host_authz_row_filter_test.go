package wasm

import (
	"database/sql"
	"fmt"
	"slices"
	"strings"
	"testing"

	abiv1 "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/djangbahevans/goerp/internal/engine/abi"
	"github.com/djangbahevans/goerp/internal/engine/manifest"
	"github.com/djangbahevans/goerp/internal/engine/permission"
	"github.com/djangbahevans/goerp/internal/engine/policy"
	"github.com/djangbahevans/goerp/internal/engine/tenantschema"
	"github.com/djangbahevans/goerp/sdk/go/model"
)

func invoiceRowFilterContext(slug string, policies []manifest.Policy, granted bool) *ModuleContext {
	permReg := permission.NewPermissionRegistry()
	permReg.Register("testmodule", []manifest.Permission{{Name: invoiceReadPerm}})
	var set permission.PermissionBitfield
	if idx, ok := permReg.Index(invoiceReadPerm); ok && granted {
		set.Set(idx)
	}

	reg := policy.New()
	reg.Register(policies, map[string][]model.ModelDeclaration{"testmodule": {invoiceModelDecl()}})
	return NewModuleContext("req-1", "testmodule", policyUserID, "", []string{"sales_manager"}, set,
		"tenant-id-1", slug, "trace-1", abi.CapAuthzCheck, nil,
		ModuleSnapshot{PermissionRegistry: permReg, PolicyRegistry: reg})
}

func TestRowFilter_Shapes(t *testing.T) {
	own := manifest.Policy{Name: "own", AppliesTo: invoiceReadPerm, Condition: "record.owner_id = current_user.id"}
	notVoid := manifest.Policy{Name: "not_void", AppliesTo: invoiceReadPerm, Condition: "record.state != 'void'"}
	restrictive := manifest.Policy{Name: "r_not_void", AppliesTo: invoiceReadPerm, Condition: "record.state != 'void'", Combine: "AND"}

	tests := []struct {
		name     string
		policies []manifest.Policy
		granted  bool
		table    string
		wantSQL  string
		wantArgs []any
	}{
		{"caller lacks the permission", []manifest.Policy{own}, false, "invoice", "AND FALSE", nil},
		{"no policy scopes the permission", nil, true, "invoice", "", nil},
		{"policies on another table", []manifest.Policy{own}, true, "other_table", "", nil},
		{"one policy", []manifest.Policy{own}, true, "invoice", `AND (("owner_id" = $1))`, []any{policyUserID}},
		{"policies combine with OR and share one parameter list", []manifest.Policy{own, notVoid}, true, "invoice",
			`AND (("owner_id" = $1) OR ("state" != $2))`, []any{policyUserID, "void"}},
		{"a restrictive policy is ANDed with the permissive group", []manifest.Policy{own, restrictive}, true, "invoice",
			`AND ((("owner_id" = $1)) AND ("state" != $2))`, []any{policyUserID, "void"}},
		{"only restrictive policies admit nothing", []manifest.Policy{restrictive}, true, "invoice", "AND FALSE", nil},
		{"a restrictive policy on another table is ignored", []manifest.Policy{own}, true, "other_table", "", nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			mc := invoiceRowFilterContext("rowfilter", tc.policies, tc.granted)

			out, hostErr := rowFilter(mc, abiv1.AuthzRowFilterInput{TableName: tc.table, Permission: invoiceReadPerm})

			if hostErr != nil {
				t.Fatalf("rowFilter: %+v", hostErr)
			}
			if out.SQL != tc.wantSQL || fmt.Sprint(out.Params) != fmt.Sprint(tc.wantArgs) {
				t.Errorf("rowFilter = %q %v, want %q %v", out.SQL, out.Params, tc.wantSQL, tc.wantArgs)
			}
		})
	}
}

func TestRowFilter_APolicyItCannotCompileIsNeverUnrestricted(t *testing.T) {
	broken := manifest.Policy{Name: "broken", AppliesTo: invoiceReadPerm, Condition: "record.state LIKE 'a%'"}
	unparsable := manifest.Policy{Name: "unparsable", AppliesTo: invoiceReadPerm, Condition: "record.state ="}

	for _, p := range []manifest.Policy{broken, unparsable} {
		mc := invoiceRowFilterContext("rowfilter", []manifest.Policy{p}, true)

		out, hostErr := rowFilter(mc, abiv1.AuthzRowFilterInput{TableName: "invoice", Permission: invoiceReadPerm})

		if hostErr == nil || hostErr.Code != abiv1.ErrCodeAuthzPolicyEvaluation || out.SQL != "" {
			t.Errorf("%s: rowFilter = %q, %+v, want a policy_evaluation_failed error", p.Name, out.SQL, hostErr)
		}
	}
}

// TestRowFilter_SelectsTheRowsRLSLetsThrough runs the fragment as an ordinary
// query and requires it to select exactly the records the tenant role can see
// under the RLS policies installed from the same conditions.
func TestRowFilter_SelectsTheRowsRLSLetsThrough(t *testing.T) {
	db, slug := newInvoiceFixture(t)
	grantFixtureTables(t, db, slug, "invoice")
	schema := tenantschema.Name(slug)
	if _, err := db.ExecContext(t.Context(), fmt.Sprintf(`ALTER TABLE %s.invoice ENABLE ROW LEVEL SECURITY`, schema)); err != nil {
		t.Fatalf("enable RLS: %v", err)
	}

	own := manifest.Policy{Name: "own", AppliesTo: invoiceReadPerm, Condition: "record.owner_id = current_user.id"}
	notVoid := manifest.Policy{Name: "not_void", AppliesTo: invoiceReadPerm, Condition: "record.state != 'void'"}
	manager := manifest.Policy{Name: "managers", AppliesTo: invoiceReadPerm, Condition: "user_has_role('sales_manager')"}
	auditor := manifest.Policy{Name: "auditors", AppliesTo: invoiceReadPerm, Condition: "user_has_role('auditor')"}
	openOwn := manifest.Policy{Name: "open_own", AppliesTo: invoiceReadPerm, Condition: "record.owner_id = current_user.id AND NOT record.state = 'void'"}
	noContact := manifest.Policy{Name: "by_contact", AppliesTo: invoiceReadPerm, Condition: "record.owner_id = current_user.contact_id"}
	rNotVoid := manifest.Policy{Name: "r_not_void", AppliesTo: invoiceReadPerm, Condition: "record.state != 'void'", Combine: "AND"}
	rAuditor := manifest.Policy{Name: "r_auditors", AppliesTo: invoiceReadPerm, Condition: "user_has_role('auditor')", Combine: "AND"}
	rOwn := manifest.Policy{Name: "r_own", AppliesTo: invoiceReadPerm, Condition: "record.owner_id = current_user.id", Combine: "AND"}

	sets := map[string][]manifest.Policy{
		"ownership":                            {own},
		"not void":                             {notVoid},
		"role held":                            {manager},
		"role not held":                        {auditor},
		"ownership or role not held":           {own, auditor},
		"ownership or not void":                {own, notVoid},
		"compound condition":                   {openOwn},
		"a user attribute that is NULL":        {noContact},
		"mixed":                                {auditor, own, notVoid},
		"restrictive narrows a permissive one": {notVoid, rOwn},
		"restrictive role not held":            {manager, rAuditor},
		"restrictive only":                     {rNotVoid},
		"restrictive and a permissive that admits all": {manager, rNotVoid},
		"two restrictive policies":                     {manager, rNotVoid, rOwn},
		"permissive or, narrowed by a restrictive":     {own, auditor, rNotVoid},
	}
	for name, set := range sets {
		t.Run(name, func(t *testing.T) {
			dropPolicies(t, db, schema)
			for _, p := range set {
				installRLSPolicy(t, db, schema, p)
			}
			mc := invoiceRowFilterContext(slug, set, true)

			out, hostErr := rowFilter(mc, abiv1.AuthzRowFilterInput{TableName: "invoice", Permission: invoiceReadPerm})
			if hostErr != nil {
				t.Fatalf("rowFilter: %+v", hostErr)
			}

			for _, id := range []string{ownInvoiceID, otherInvoiceID, voidInvoiceID} {
				inFragment := selectedByFragment(t, db, schema, id, out)
				if visible := visibleUnderRLS(t, db, mc, id); inFragment != visible {
					t.Errorf("record %s: fragment selects = %v, RLS visible = %v (fragment %q)", id, inFragment, visible, out.SQL)
				}
			}
		})
	}
}

func selectedByFragment(t *testing.T, db *sql.DB, schema, id string, f abiv1.AuthzRowFilterOutput) bool {
	t.Helper()
	query := fmt.Sprintf(`SELECT count(*) FROM %s.invoice WHERE id = $%d %s`, schema, len(f.Params)+1, f.SQL)
	args := append(slices.Clone(f.Params), id)

	var n int
	if err := db.QueryRowContext(t.Context(), query, args...).Scan(&n); err != nil {
		t.Fatalf("%s: %v", strings.TrimSpace(query), err)
	}
	return n == 1
}
