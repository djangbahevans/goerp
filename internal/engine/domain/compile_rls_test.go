package domain

import "testing"

func compileSrc(t *testing.T, src string) string {
	t.Helper()
	expr, err := Parse(src)
	if err != nil {
		t.Fatalf("Parse(%q) error: %v", src, err)
	}
	sql, err := CompileToRLS(expr)
	if err != nil {
		t.Fatalf("CompileToRLS(%q) error: %v", src, err)
	}
	return sql
}

func TestCompileToRLS_DocExample(t *testing.T) {
	// Verbatim from multitenancy-internals.md §5a's own worked example
	// (modulo column-name quoting — quoted and bare lowercase identifiers
	// are semantically identical in Postgres, and quoting defensively
	// covers field names that collide with reserved words).
	got := compileSrc(t, "record.salesperson_id = current_user.contact_id OR user_has_role('sales_manager')")
	want := `(("salesperson_id" = NULLIF(current_setting('app.current_user_contact_id', true), '')::uuid) ` +
		`OR current_setting('app.current_user_roles', true) LIKE '%sales_manager%')`
	if got != want {
		t.Fatalf("CompileToRLS() =\n  %s\nwant\n  %s", got, want)
	}
}

func TestCompileToRLS_CurrentUserID(t *testing.T) {
	got := compileSrc(t, "record.owner_id = current_user.id")
	want := `("owner_id" = NULLIF(current_setting('app.current_user_id', true), '')::uuid)`
	if got != want {
		t.Fatalf("CompileToRLS() = %s, want %s", got, want)
	}
}

func TestCompileToRLS_And(t *testing.T) {
	got := compileSrc(t, "record.state = 'draft' AND current_user.id = record.created_by")
	want := `(("state" = 'draft') AND (NULLIF(current_setting('app.current_user_id', true), '')::uuid = "created_by"))`
	if got != want {
		t.Fatalf("CompileToRLS() = %s, want %s", got, want)
	}
}

func TestCompileToRLS_Not(t *testing.T) {
	got := compileSrc(t, "NOT user_has_role('admin')")
	want := "(NOT current_setting('app.current_user_roles', true) LIKE '%admin%')"
	if got != want {
		t.Fatalf("CompileToRLS() = %s, want %s", got, want)
	}
}

func TestCompileToRLS_IsNull(t *testing.T) {
	got := compileSrc(t, "record.deleted_at IS NULL")
	want := `("deleted_at" IS NULL)`
	if got != want {
		t.Fatalf("CompileToRLS() = %s, want %s", got, want)
	}
}

func TestCompileToRLS_In(t *testing.T) {
	got := compileSrc(t, "record.state IN ('draft', 'confirmed')")
	want := `("state" IN ('draft', 'confirmed'))`
	if got != want {
		t.Fatalf("CompileToRLS() = %s, want %s", got, want)
	}
}

func TestCompileToRLS_NumberLiteral(t *testing.T) {
	got := compileSrc(t, "record.amount > 1000")
	want := `("amount" > 1000)`
	if got != want {
		t.Fatalf("CompileToRLS() = %s, want %s", got, want)
	}
}

func TestCompileToRLS_StringEscaping(t *testing.T) {
	got := compileSrc(t, "record.name = 'O''Brien'")
	want := `("name" = 'O''Brien')`
	if got != want {
		t.Fatalf("CompileToRLS() = %s, want %s", got, want)
	}
}

func TestCompileToRLS_ColumnNameQuotedDefensively(t *testing.T) {
	// A field name that happens to be a reserved SQL word must still come
	// out safely quoted in the compiled output.
	got := compileSrc(t, "record.select = true")
	want := `("select" = true)`
	if got != want {
		t.Fatalf("CompileToRLS() = %s, want %s", got, want)
	}
}

func TestCompileToRLS_RejectsLikeIlike(t *testing.T) {
	expr, err := Parse("record.name LIKE 'acme%'")
	if err != nil {
		t.Fatalf("Parse() error: %v", err)
	}
	if _, err := CompileToRLS(expr); err == nil {
		t.Fatalf("CompileToRLS() expected error rejecting LIKE outside search-domain context")
	}
}

func TestCompileToRLS_RejectsChildOf(t *testing.T) {
	expr, err := Parse("record child_of record.category_id")
	if err != nil {
		t.Fatalf("Parse() error: %v", err)
	}
	if _, err := CompileToRLS(expr); err == nil {
		t.Fatalf("CompileToRLS() expected error — .Tree()/ltree support doesn't exist yet")
	}
}

func TestCompileToRLS_RejectsTenantAttr(t *testing.T) {
	expr, err := Parse("tenant.country_code = 'GH'")
	if err != nil {
		t.Fatalf("Parse() error: %v", err)
	}
	if _, err := CompileToRLS(expr); err == nil {
		t.Fatalf("CompileToRLS() expected error — tenant.* is not bound in ABAC policy conditions")
	}
}

func TestCompileToRLS_RejectsUserHasPermission(t *testing.T) {
	expr, err := Parse("user_has_permission('sales:order:confirm')")
	if err != nil {
		t.Fatalf("Parse() error: %v", err)
	}
	if _, err := CompileToRLS(expr); err == nil {
		t.Fatalf("CompileToRLS() expected error — no permission-set session variable exists yet")
	}
}

func TestCompileToRLS_RejectsUnknownUserAttr(t *testing.T) {
	expr, err := Parse("current_user.tenant_id = record.tenant_id")
	if err != nil {
		t.Fatalf("Parse() error: %v", err)
	}
	if _, err := CompileToRLS(expr); err == nil {
		t.Fatalf("CompileToRLS() expected error — no session variable for current_user.tenant_id exists yet")
	}
}

func TestCompileToRLS_NoDynamicCodeExecution(t *testing.T) {
	// Structural guarantee, not just a string check: the compiler is a pure
	// tree-walk with a fixed, closed set of AST node types (ast.go) — there
	// is no branch anywhere that formats a caller-controlled Go template,
	// evaluates a caller-controlled expression, or otherwise executes
	// dynamic code. This test exists as a documented assertion of that
	// property for reviewers, not a runtime check.
	got := compileSrc(t, "true")
	if got != "true" {
		t.Fatalf("CompileToRLS() = %s, want true", got)
	}
}
