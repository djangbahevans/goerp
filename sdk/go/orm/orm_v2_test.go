package orm

import "testing"

// testModel and otherModel back the sub-issue #973 unit tests below —
// two distinct Model implementations, so a test can assert that a
// Field/Condition built against one can't be used where the other is
// expected.
type testModel struct{}

func (testModel) ResourceName() string { return "test.model" }

type otherModel struct{}

func (otherModel) ResourceName() string { return "other.model" }

// TestField_IndependentAcrossModels is the runtime half of the cross-
// model compile-fail example below: the same field name on two distinct
// Model type parameters produces two distinct Condition types, each
// usable only for its own model.
func TestField_IndependentAcrossModels(t *testing.T) {
	name := NewField[testModel, string]("name")
	otherName := NewField[otherModel, string]("name")

	c := name.Eq("Acme")
	oc := otherName.Eq("Acme")

	if c.expr != oc.expr {
		t.Errorf("expr = %q, otherExpr = %q — expected identical rendering, distinct types", c.expr, oc.expr)
	}
}

// Cross-model type mismatch fails to compile (issue #973's acceptance
// criteria) — this can't be exercised as a runtime test, since a Go
// program that violates it doesn't build at all. Documented example
// instead:
//
//	name := NewField[testModel, string]("name")
//	other := NewField[otherModel, string]("name")
//
//	var c Condition[testModel]
//	c = name.Eq("Acme")   // fine — testModel throughout
//	c = other.Eq("Acme")  // compile error: cannot use other.Eq("Acme")
//	                      // (value of type Condition[otherModel]) as
//	                      // Condition[testModel] value in assignment
//
// Likewise Sum/Avg (issue #976) reject an OrderedField whose TValue is a
// Selection/Enum named string type — Numeric deliberately excludes it
// even though it satisfies the broader Ordered constraint OrderedField
// itself requires:
//
//	type ProductStatus string // as goerp module generate emits (goerp#977)
//	status := NewOrderedField[testModel, ProductStatus]("status")
//	_, _ = Sum(status, MatchAll[testModel]()) // compile error:
//	                      // ProductStatus does not implement Numeric
//	                      // (string missing in ProductStatus's type set)
//
// Sum/Avg also reject a field that isn't an OrderedField at all:
//
//	name := NewStringField[testModel]("name")
//	_, _ = Sum(name, MatchAll[testModel]()) // compile error: StringField
//	                      // is not OrderedField[testModel, TValue] for
//	                      // any TValue
//
// Min/Max (issue #976) reject a field that's neither an OrderedField nor
// a TimeField — a plain Field[TModel, TValue] (e.g. a boolean or
// relation-ID column) or a BytesField has no meaningful minimum/maximum:
//
//	active := NewField[testModel, bool]("is_active")
//	_, _ = Min(active, MatchAll[testModel]()) // compile error:
//	                      // Field[testModel, bool] does not implement
//	                      // Sortable[testModel] (missing isSortable)
