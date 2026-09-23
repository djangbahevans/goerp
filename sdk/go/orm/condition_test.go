package orm

import "testing"

func TestCondition_And(t *testing.T) {
	a := NewField[testModel, string]("type").Eq("person")
	b := NewField[testModel, bool]("is_active").Eq(true)

	got := a.And(b).expr
	want := "(record.type = 'person') AND (record.is_active = true)"
	if got != want {
		t.Errorf("And() = %q, want %q", got, want)
	}
}

func TestCondition_Or(t *testing.T) {
	a := NewField[testModel, string]("type").Eq("person")
	b := NewField[testModel, string]("type").Eq("company")

	got := a.Or(b).expr
	want := "(record.type = 'person') OR (record.type = 'company')"
	if got != want {
		t.Errorf("Or() = %q, want %q", got, want)
	}
}

// TestCondition_OrThenAnd pins And's defensive parenthesization of an
// Or-built operand: without it, "(type = 'person') OR (type = 'company')
// AND is_active = true" would parse (AND binds tighter than OR,
// manifest-spec.md §8) as "(type = 'person') OR ((type = 'company') AND
// is_active = true)" — not the "(either type) AND is_active" grouping
// the call chain expresses.
func TestCondition_OrThenAnd(t *testing.T) {
	person := NewField[testModel, string]("type").Eq("person")
	company := NewField[testModel, string]("type").Eq("company")
	active := NewField[testModel, bool]("is_active").Eq(true)

	got := person.Or(company).And(active).expr
	want := "((record.type = 'person') OR (record.type = 'company')) AND (record.is_active = true)"
	if got != want {
		t.Errorf("Or().And() = %q, want %q", got, want)
	}
}

func TestNot(t *testing.T) {
	a := NewField[testModel, string]("type").Eq("person")

	got := Not(a).expr
	want := "NOT (record.type = 'person')"
	if got != want {
		t.Errorf("Not() = %q, want %q", got, want)
	}
}

func TestRaw(t *testing.T) {
	got := Raw[testModel]("child_of record").expr
	want := "child_of record"
	if got != want {
		t.Errorf("Raw() = %q, want %q", got, want)
	}
}

func TestMatchAll(t *testing.T) {
	got := MatchAll[testModel]().expr
	want := "true"
	if got != want {
		t.Errorf("MatchAll() = %q, want %q", got, want)
	}
}

func TestBind(t *testing.T) {
	c := NewField[testModel, string]("type").Eq("person")

	bound := c.Bind()
	if bound.expr != c.expr {
		t.Errorf("Bind().expr = %q, want %q", bound.expr, c.expr)
	}
}

func TestUnbounded(t *testing.T) {
	got := Unbounded[testModel]()
	want := MatchAll[testModel]().Bind()
	if got.expr != want.expr {
		t.Errorf("Unbounded() = %q, want %q", got.expr, want.expr)
	}
}
