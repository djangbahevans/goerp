package domain

// Expr is a node in a parsed domain expression AST. See manifest-spec.md §8
// for the grammar and auth-internals.md §11 "Policy expression evaluator"
// for the canonical token-to-AST-node mapping this package implements.
type Expr interface {
	isExpr()
}

// RecordField is `record.{field}`.
type RecordField struct{ Field string }

// UserAttr is `current_user.{attr}` / `user.{attr}` — attr is one of
// "id", "contact_id", "tenant_id".
type UserAttr struct{ Attr string }

// TenantAttr is `tenant.{field}` — only valid in tenant-only contexts
// (report_overrides[].condition); rejected by CompileToRLS.
type TenantAttr struct{ Field string }

// RoleCheck is `user_has_role('{role}')`.
type RoleCheck struct{ Role string }

// PermCheck is `user_has_permission('{permission}')`.
type PermCheck struct{ Perm string }

// Literal is a `true`/`false`/`null`/string/number literal. Value is one
// of nil, bool, string (a quoted string literal), or Number (a bare
// numeric literal) — kept as a distinct type from string so a quoted
// string that happens to look numeric (e.g. `'123'`) isn't mistaken for
// the bare numeric literal `123`.
type Literal struct{ Value any }

// Number is a bare numeric literal's source text, kept unparsed since the
// compiler only ever splices it verbatim into generated SQL.
type Number string

// BinaryExpr covers the infix comparison, LIKE/ILIKE, AND, and OR
// operators. Op is one of "=", "!=", "<", ">", "<=", ">=", "LIKE",
// "ILIKE", "AND", "OR".
type BinaryExpr struct {
	Op    string
	Left  Expr
	Right Expr
}

// UnaryExpr is `NOT {expr}`.
type UnaryExpr struct {
	Op      string // "NOT"
	Operand Expr
}

// IsNullExpr is `{expr} IS NULL` / `{expr} IS NOT NULL`.
type IsNullExpr struct {
	Operand Expr
	Not     bool
}

// InExpr is `{expr} IN (val, val, ...)`.
type InExpr struct {
	Operand Expr
	Values  []Expr
}

// TreeExpr is `record child_of {expr}` / `record parent_of {expr}` —
// .Tree()-field descendant/ancestor checks.
type TreeExpr struct {
	Op     string // "child_of" or "parent_of"
	Target Expr
}

func (RecordField) isExpr() {}
func (UserAttr) isExpr()    {}
func (TenantAttr) isExpr()  {}
func (RoleCheck) isExpr()   {}
func (PermCheck) isExpr()   {}
func (Literal) isExpr()     {}
func (BinaryExpr) isExpr()  {}
func (UnaryExpr) isExpr()   {}
func (IsNullExpr) isExpr()  {}
func (InExpr) isExpr()      {}
func (TreeExpr) isExpr()    {}
