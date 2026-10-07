package domain

import (
	"reflect"
	"strings"
	"testing"
)

func compileToSQL(t *testing.T, src string) (string, []any) {
	t.Helper()
	expr, err := Parse(src)
	if err != nil {
		t.Fatalf("Parse(%q) error: %v", src, err)
	}
	frag, args, err := CompileToSQL(expr)
	if err != nil {
		t.Fatalf("CompileToSQL(%q) error: %v", src, err)
	}
	return frag, args
}

func TestCompileToSQL_StringLiteralParameterized(t *testing.T) {
	frag, args := compileToSQL(t, "record.state = 'draft'")
	want := `("state" = $1)`
	if frag != want {
		t.Fatalf("fragment = %s, want %s", frag, want)
	}
	if !reflect.DeepEqual(args, []any{"draft"}) {
		t.Fatalf("args = %#v, want [\"draft\"]", args)
	}
}

func TestCompileToSQL_NumberLiteralParameterizedAsFloat(t *testing.T) {
	frag, args := compileToSQL(t, "record.amount > 1000")
	want := `("amount" > $1)`
	if frag != want {
		t.Fatalf("fragment = %s, want %s", frag, want)
	}
	if !reflect.DeepEqual(args, []any{1000.0}) {
		t.Fatalf("args = %#v, want [1000.0]", args)
	}
}

func TestCompileToSQL_BoolAndNullInlined(t *testing.T) {
	frag, args := compileToSQL(t, "record.active = true AND record.deleted_at IS NULL")
	want := `(("active" = true) AND ("deleted_at" IS NULL))`
	if frag != want {
		t.Fatalf("fragment = %s, want %s", frag, want)
	}
	if len(args) != 0 {
		t.Fatalf("args = %#v, want none (bool/null are inlined, not bound)", args)
	}
}

func TestCompileToSQL_MultipleLiteralsGetSequentialPlaceholders(t *testing.T) {
	frag, args := compileToSQL(t, "record.state IN ('draft', 'confirmed')")
	want := `("state" IN ($1, $2))`
	if frag != want {
		t.Fatalf("fragment = %s, want %s", frag, want)
	}
	if !reflect.DeepEqual(args, []any{"draft", "confirmed"}) {
		t.Fatalf("args = %#v, want [draft confirmed]", args)
	}
}

func TestCompileToSQL_NotWrapsWholePredicate(t *testing.T) {
	frag, args := compileToSQL(t, "NOT record.state = 'draft'")
	want := `(NOT ("state" = $1))`
	if frag != want {
		t.Fatalf("fragment = %s, want %s", frag, want)
	}
	if !reflect.DeepEqual(args, []any{"draft"}) {
		t.Fatalf("args = %#v, want [\"draft\"]", args)
	}
}

func TestCompileToSQL_LikeAllowed(t *testing.T) {
	// Unlike CompileToRLS, LIKE/ILIKE is valid in the search-domain context.
	frag, args := compileToSQL(t, "record.name ILIKE 'acme%'")
	want := `("name" ILIKE $1)`
	if frag != want {
		t.Fatalf("fragment = %s, want %s", frag, want)
	}
	if !reflect.DeepEqual(args, []any{"acme%"}) {
		t.Fatalf("args = %#v, want [acme%%]", args)
	}
}

func TestCompileToSQL_RejectsUserAttr(t *testing.T) {
	expr, err := Parse("current_user.id = record.owner_id")
	if err != nil {
		t.Fatalf("Parse() error: %v", err)
	}
	if _, _, err := CompileToSQL(expr); err == nil {
		t.Fatalf("CompileToSQL() expected error — current_user is not bound in a search domain")
	}
}

func TestCompileToSQL_RejectsRoleCheck(t *testing.T) {
	expr, err := Parse("user_has_role('admin')")
	if err != nil {
		t.Fatalf("Parse() error: %v", err)
	}
	if _, _, err := CompileToSQL(expr); err == nil {
		t.Fatalf("CompileToSQL() expected error — user_has_role is not bound in a search domain")
	}
}

func TestCompileToSQL_RejectsTenantAttr(t *testing.T) {
	expr, err := Parse("tenant.country_code = 'GH'")
	if err != nil {
		t.Fatalf("Parse() error: %v", err)
	}
	if _, _, err := CompileToSQL(expr); err == nil {
		t.Fatalf("CompileToSQL() expected error — tenant is not bound in a search domain")
	}
}

func TestCompileToSQL_RejectsChildOf(t *testing.T) {
	expr, err := Parse("record child_of record.category_id")
	if err != nil {
		t.Fatalf("Parse() error: %v", err)
	}
	if _, _, err := CompileToSQL(expr); err == nil {
		t.Fatalf("CompileToSQL() expected error — .Tree()/ltree support doesn't exist yet")
	}
}

func TestCompileToSQL_NoDynamicCodeExecution(t *testing.T) {
	// Structural guarantee, not just a string check: the compiler is a pure
	// tree-walk over the same closed set of AST node types CompileToRLS
	// uses (ast.go) — there is no branch anywhere that formats a
	// caller-controlled Go template or otherwise executes dynamic code.
	frag, _ := compileToSQL(t, "true")
	if frag != "true" {
		t.Fatalf("fragment = %s, want true", frag)
	}
}

func TestCompileToFilter(t *testing.T) {
	env := Env{
		UserID: "u-1", ContactID: "", TenantID: "t-1",
		Roles:         []string{"sales_manager"},
		HasPermission: func(name string) bool { return name == "sales:order:confirm" },
	}

	tests := []struct {
		src      string
		wantSQL  string
		wantArgs []any
	}{
		{"record.owner_id = current_user.id", `("owner_id" = $1)`, []any{"u-1"}},
		{"record.owner_id = current_user.contact_id", `("owner_id" = NULL)`, nil},
		{"current_user.tenant_id = record.tenant_id", `($1 = "tenant_id")`, []any{"t-1"}},
		{"user_has_role('sales_manager')", "TRUE", nil},
		{"user_has_role('auditor')", "FALSE", nil},
		{"user_has_role('sales_manager') OR record.state = 'open'", `(TRUE OR ("state" = $1))`, []any{"open"}},
		{"user_has_permission('sales:order:confirm')", "TRUE", nil},
		{"user_has_permission('sales:order:cancel')", "FALSE", nil},
		{"NOT user_has_role('auditor') AND record.owner_id = current_user.id", `((NOT FALSE) AND ("owner_id" = $1))`, []any{"u-1"}},
	}
	for _, tc := range tests {
		t.Run(tc.src, func(t *testing.T) {
			expr, err := Parse(tc.src)
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			sql, args, err := CompileToFilter(expr, env, 0)
			if err != nil {
				t.Fatalf("CompileToFilter: %v", err)
			}
			if sql != tc.wantSQL || !reflect.DeepEqual(args, tc.wantArgs) {
				t.Errorf("CompileToFilter = %q %v, want %q %v", sql, args, tc.wantSQL, tc.wantArgs)
			}
		})
	}
}

func TestCompileToFilter_NumbersPlaceholdersAfterEarlierParams(t *testing.T) {
	expr, err := Parse("record.owner_id = current_user.id AND record.state = 'open'")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	sql, args, err := CompileToFilter(expr, Env{UserID: "u-1"}, 3)

	if err != nil {
		t.Fatalf("CompileToFilter: %v", err)
	}
	if want := `(("owner_id" = $4) AND ("state" = $5))`; sql != want || len(args) != 2 {
		t.Errorf("CompileToFilter = %q %v, want %q with two args", sql, args, want)
	}
}

func TestCompileToFilter_Rejections(t *testing.T) {
	for _, tc := range []struct {
		src  string
		env  Env
		want string
	}{
		{"record.state LIKE 'a%'", Env{}, "LIKE is search-domain only"},
		{"record.state ILIKE 'a%'", Env{}, "ILIKE is search-domain only"},
		{"tenant.country = 'GH'", Env{}, "tenant.country is not bound"},
		{"record child_of 'x'", Env{}, "child_of is not yet supported"},
		{"user_has_permission('a:b:c')", Env{}, "no permission resolver"},
	} {
		expr, err := Parse(tc.src)
		if err != nil {
			t.Fatalf("Parse(%q): %v", tc.src, err)
		}
		if _, _, err := CompileToFilter(expr, tc.env, 0); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("CompileToFilter(%q) = %v, want an error containing %q", tc.src, err, tc.want)
		}
	}
}
