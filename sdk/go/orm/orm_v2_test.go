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
// Likewise a NumericField can't be obtained from an OrderedField whose
// TValue is a Selection/Enum named string type:
//
//	type ProductStatus string // as goerp module generate emits (goerp#977)
//	status := NewOrderedField[testModel, ProductStatus]("status")
//	_ = AsNumeric(status) // compile error: ProductStatus does not
//	                      // implement Numeric (string missing in
//	                      // ProductStatus's type set)
