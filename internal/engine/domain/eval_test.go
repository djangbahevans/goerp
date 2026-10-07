package domain

import (
	"strings"
	"testing"
	"time"
)

const (
	userA = "11111111-1111-1111-1111-111111111111"
	userB = "22222222-2222-2222-2222-222222222222"
)

func evalSrc(t *testing.T, src string, env Env) (bool, error) {
	t.Helper()
	expr, err := Parse(src)
	if err != nil {
		t.Fatalf("Parse(%q): %v", src, err)
	}
	return Eval(expr, env)
}

func TestEval_Verdicts(t *testing.T) {
	env := Env{
		Record: map[string]any{
			"state": "draft", "qty": int64(5), "price": 9.5, "paid": true, "note": nil,
			"owner_id": userA, "created_at": time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC),
		},
		UserID: userA, ContactID: userB, TenantID: "t-1",
		Roles:         []string{"sales_manager"},
		HasPermission: func(name string) bool { return name == "sales:order:confirm" },
	}

	tests := []struct {
		src  string
		want bool
	}{
		{"record.state = 'draft'", true},
		{"record.state != 'draft'", false},
		{"record.qty > 4 AND record.qty <= 5", true},
		{"record.qty < 5 OR record.qty >= 6", false},
		{"record.qty = 5.0", true},
		{"record.price >= 9.5", true},
		{"record.paid = true", true},
		{"NOT record.paid", false},
		{"record.owner_id = current_user.id", true},
		{"record.owner_id = current_user.contact_id", false},
		{"current_user.tenant_id = 't-1'", true},
		{"user.id = record.owner_id", true},
		{"user_has_role('sales_manager')", true},
		{"user_has_role('sales')", false},
		{"user_has_permission('sales:order:confirm')", true},
		{"user_has_permission('sales:order:cancel')", false},
		{"record.note IS NULL", true},
		{"record.note IS NOT NULL", false},
		{"record.state IS NOT NULL", true},
		{"record.state IN ('draft', 'confirmed')", true},
		{"record.state IN ('cancelled')", false},
		{"record.qty IN (1, 5)", true},
		{"record.created_at > '2026-01-01T00:00:00Z'", true},
		{"record.created_at < '2026-03-01T12:00:00Z'", false},
		{"record.owner_id = current_user.contact_id OR user_has_role('sales_manager')", true},
		// SQL three-valued logic: a comparison with NULL is unknown, never true.
		{"record.note = 'x'", false},
		{"record.note != 'x'", false},
		{"NOT (record.note = 'x')", false},
		{"record.note = 'x' OR record.state = 'draft'", true},
		{"record.note = 'x' AND record.state = 'draft'", false},
		{"record.note = 'x' OR record.state = 'other'", false},
		{"record.note IN ('x')", false},
		{"record.state IN ('x', null)", false},
		{"record.state IN ('draft', null)", true},
		{"record.state = null", false},
	}
	for _, tc := range tests {
		t.Run(tc.src, func(t *testing.T) {
			got, err := evalSrc(t, tc.src, env)
			if err != nil {
				t.Fatalf("Eval: %v", err)
			}
			if got != tc.want {
				t.Errorf("Eval(%q) = %v, want %v", tc.src, got, tc.want)
			}
		})
	}
}

func TestEval_EmptyUserAttributeIsNull(t *testing.T) {
	env := Env{Record: map[string]any{"owner_id": userA}}

	got, err := evalSrc(t, "record.owner_id = current_user.contact_id", env)
	if err != nil || got {
		t.Errorf("Eval with no contact = %v, %v, want false with no error", got, err)
	}
	got, err = evalSrc(t, "current_user.contact_id IS NULL", env)
	if err != nil || !got {
		t.Errorf("IS NULL on an empty contact ID = %v, %v, want true", got, err)
	}
}

func TestEval_NormalizesRecordValueTypes(t *testing.T) {
	var nilID *pointerStringer
	env := Env{Record: map[string]any{"n": 7, "u": uint8(7), "f": float32(2.5), "id": stringer("abc"), "nil_id": nilID}}

	for _, src := range []string{"record.n = 7", "record.u = 7", "record.f = 2.5", "record.id = 'abc'", "record.nil_id IS NULL"} {
		if got, err := evalSrc(t, src, env); err != nil || !got {
			t.Errorf("Eval(%q) = %v, %v, want true", src, got, err)
		}
	}
}

type stringer string

func (s stringer) String() string { return string(s) }

type pointerStringer struct{ id string }

func (p *pointerStringer) String() string { return p.id }

func TestEval_Errors(t *testing.T) {
	env := Env{Record: map[string]any{"state": "draft", "qty": int64(1), "blob": []int{1}, "at": time.Now()}}

	tests := []struct {
		name string
		src  string
		want string
	}{
		{"missing field", "record.missing = 1", `record has no field "missing"`},
		{"incompatible types", "record.state > 3", "cannot compare string with int64"},
		{"bad date", "record.at > 'yesterday'", "not an RFC 3339 timestamp"},
		{"non-boolean result", "record.state", "not a boolean"},
		{"non-boolean operand", "record.state AND record.qty = 1", "not a boolean operand"},
		{"LIKE", "record.state LIKE 'd%'", "LIKE is search-domain only"},
		{"ILIKE", "record.state ILIKE 'd%'", "ILIKE is search-domain only"},
		{"child_of", "record child_of 'x'", "child_of is not supported"},
		{"parent_of", "record parent_of 'x'", "parent_of is not supported"},
		{"tenant attribute", "tenant.country = 'GH'", "tenant-only contexts"},
		{"no permission resolver", "user_has_permission('a:b:c')", "no permission resolver"},
		{"bare date", "record.at > '2026-01-01'", "not an RFC 3339 timestamp"},
		{"unrepresentable record value", "record.blob = 1", "no comparable form"},
		{"unknown user attribute", "current_user.email = 'x'", "current_user.email is not bound"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := evalSrc(t, tc.src, env)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Eval(%q) = %v, %v, want an error containing %q", tc.src, got, err, tc.want)
			}
			if got {
				t.Errorf("Eval(%q) allowed alongside an error", tc.src)
			}
		})
	}
}

func TestEval_BothSidesOfALogicalOperatorAreChecked(t *testing.T) {
	env := Env{Record: map[string]any{"state": "draft"}}

	if _, err := evalSrc(t, "record.state = 'draft' OR record.missing = 1", env); err == nil {
		t.Error("a true left operand hid an error in the right operand")
	}
}
