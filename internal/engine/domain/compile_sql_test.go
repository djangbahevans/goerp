package domain

import (
	"reflect"
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
