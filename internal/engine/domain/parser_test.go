package domain

import (
	"reflect"
	"testing"
)

func TestParse_RecordField(t *testing.T) {
	expr, err := Parse("record.salesperson_id = current_user.contact_id")
	if err != nil {
		t.Fatalf("Parse() error: %v", err)
	}
	want := BinaryExpr{
		Op:    "=",
		Left:  RecordField{Field: "salesperson_id"},
		Right: UserAttr{Attr: "contact_id"},
	}
	if !reflect.DeepEqual(expr, want) {
		t.Fatalf("Parse() = %#v, want %#v", expr, want)
	}
}

func TestParse_OrPrecedenceOverAnd(t *testing.T) {
	// AND binds tighter than OR: `a OR b AND c` parses as `a OR (b AND c)`.
	expr, err := Parse("true OR false AND false")
	if err != nil {
		t.Fatalf("Parse() error: %v", err)
	}
	or, ok := expr.(BinaryExpr)
	if !ok || or.Op != "OR" {
		t.Fatalf("top-level op = %#v, want OR", expr)
	}
	and, ok := or.Right.(BinaryExpr)
	if !ok || and.Op != "AND" {
		t.Fatalf("right operand = %#v, want AND", or.Right)
	}
}

func TestParse_ComparisonBindsTighterThanLike(t *testing.T) {
	// Per manifest-spec.md §8 precedence: comparison > LIKE, so
	// `a = b LIKE c` parses as `(a = b) LIKE c`.
	expr, err := Parse("record.a = record.b LIKE 'x%'")
	if err != nil {
		t.Fatalf("Parse() error: %v", err)
	}
	like, ok := expr.(BinaryExpr)
	if !ok || like.Op != "LIKE" {
		t.Fatalf("top-level op = %#v, want LIKE", expr)
	}
	eq, ok := like.Left.(BinaryExpr)
	if !ok || eq.Op != "=" {
		t.Fatalf("left operand = %#v, want =", like.Left)
	}
}

func TestParse_Not(t *testing.T) {
	expr, err := Parse("NOT user_has_role('admin')")
	if err != nil {
		t.Fatalf("Parse() error: %v", err)
	}
	want := UnaryExpr{Op: "NOT", Operand: RoleCheck{Role: "admin"}}
	if !reflect.DeepEqual(expr, want) {
		t.Fatalf("Parse() = %#v, want %#v", expr, want)
	}
}

func TestParse_IsNull(t *testing.T) {
	expr, err := Parse("record.deleted_at IS NULL")
	if err != nil {
		t.Fatalf("Parse() error: %v", err)
	}
	want := IsNullExpr{Operand: RecordField{Field: "deleted_at"}, Not: false}
	if !reflect.DeepEqual(expr, want) {
		t.Fatalf("Parse() = %#v, want %#v", expr, want)
	}
}

func TestParse_IsNotNull(t *testing.T) {
	expr, err := Parse("record.deleted_at IS NOT NULL")
	if err != nil {
		t.Fatalf("Parse() error: %v", err)
	}
	want := IsNullExpr{Operand: RecordField{Field: "deleted_at"}, Not: true}
	if !reflect.DeepEqual(expr, want) {
		t.Fatalf("Parse() = %#v, want %#v", expr, want)
	}
}

func TestParse_In(t *testing.T) {
	expr, err := Parse("record.state IN ('draft', 'confirmed')")
	if err != nil {
		t.Fatalf("Parse() error: %v", err)
	}
	want := InExpr{
		Operand: RecordField{Field: "state"},
		Values:  []Expr{Literal{Value: "draft"}, Literal{Value: "confirmed"}},
	}
	if !reflect.DeepEqual(expr, want) {
		t.Fatalf("Parse() = %#v, want %#v", expr, want)
	}
}

func TestParse_ChildOf(t *testing.T) {
	expr, err := Parse("record child_of record.category_id")
	if err != nil {
		t.Fatalf("Parse() error: %v", err)
	}
	want := TreeExpr{Op: "child_of", Target: RecordField{Field: "category_id"}}
	if !reflect.DeepEqual(expr, want) {
		t.Fatalf("Parse() = %#v, want %#v", expr, want)
	}
}

func TestParse_ChildOf_RejectsNonRecordLHS(t *testing.T) {
	if _, err := Parse("record.foo child_of record.category_id"); err == nil {
		t.Fatalf("Parse() expected error for non-bare-record LHS of child_of")
	}
}

func TestParse_BareRecordRejectedOutsideChildOf(t *testing.T) {
	if _, err := Parse("record = current_user.id"); err == nil {
		t.Fatalf("Parse() expected error for bare `record` used outside child_of/parent_of")
	}
}

func TestParse_StringEscaping(t *testing.T) {
	expr, err := Parse("record.name = 'O''Brien'")
	if err != nil {
		t.Fatalf("Parse() error: %v", err)
	}
	bin := expr.(BinaryExpr)
	lit := bin.Right.(Literal)
	if lit.Value != "O'Brien" {
		t.Fatalf("Literal.Value = %q, want %q", lit.Value, "O'Brien")
	}
}

func TestParse_NumberVsStringLiteral(t *testing.T) {
	expr, err := Parse("record.code = '123'")
	if err != nil {
		t.Fatalf("Parse() error: %v", err)
	}
	bin := expr.(BinaryExpr)
	lit := bin.Right.(Literal)
	if _, isString := lit.Value.(string); !isString {
		t.Fatalf("Literal.Value type = %T, want string (quoted literal must stay a string, not Number)", lit.Value)
	}

	expr2, err := Parse("record.amount > 1000")
	if err != nil {
		t.Fatalf("Parse() error: %v", err)
	}
	bin2 := expr2.(BinaryExpr)
	lit2 := bin2.Right.(Literal)
	if _, isNumber := lit2.Value.(Number); !isNumber {
		t.Fatalf("Literal.Value type = %T, want Number (bare literal must not be a quoted string)", lit2.Value)
	}
}

func TestParse_FullExample(t *testing.T) {
	// From manifest-spec.md §8's own example.
	expr, err := Parse("record.salesperson_id = current_user.contact_id OR user_has_role('sales_manager')")
	if err != nil {
		t.Fatalf("Parse() error: %v", err)
	}
	or, ok := expr.(BinaryExpr)
	if !ok || or.Op != "OR" {
		t.Fatalf("top-level op = %#v, want OR", expr)
	}
}

func TestParse_Errors(t *testing.T) {
	cases := []string{
		"",
		"record.",
		"record.foo AND",
		"record.foo = ",
		"unknown_ident",
		"record.foo IN (",
		"'unterminated",
		"record.foo IS",
		"record.foo IS MAYBE",
		"user_has_role(record.foo)", // must be a string literal
		"record.foo LIKE record.bar RemainderGarbage extra tokens",
	}
	for _, src := range cases {
		if _, err := Parse(src); err == nil {
			t.Errorf("Parse(%q) expected error, got none", src)
		}
	}
}

func TestParse_Parentheses(t *testing.T) {
	expr, err := Parse("(record.a = record.b) OR record.c IS NULL")
	if err != nil {
		t.Fatalf("Parse() error: %v", err)
	}
	or, ok := expr.(BinaryExpr)
	if !ok || or.Op != "OR" {
		t.Fatalf("top-level op = %#v, want OR", expr)
	}
}
