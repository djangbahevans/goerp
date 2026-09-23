package orm

import "testing"

// crossModuleMarkerStub stands in for what goerp module generate emits
// for a cross-module Many2One target (goerp#979) — Model only, no Scan.
type crossModuleMarkerStub struct{}

func (crossModuleMarkerStub) ResourceName() string { return "other.thing" }

func TestRef_Expand(t *testing.T) {
	rel := &RelationRef{ID: "abc", DisplayName: "Acme"}
	r := Ref[valuesTestModel]{RelationRef: rel}

	if got := r.Expand(); got != rel {
		t.Errorf("Expand() = %v, want %v", got, rel)
	}
	// The embedded *RelationRef promotes its own fields.
	if r.ID != "abc" || r.DisplayName != "Acme" {
		t.Errorf("r.ID/DisplayName = %q/%q, want abc/Acme", r.ID, r.DisplayName)
	}
}

func TestRef_ZeroValueExpandsToNil(t *testing.T) {
	var r Ref[valuesTestModel]
	if got := r.Expand(); got != nil {
		t.Errorf("Expand() = %v, want nil (nothing to expand)", got)
	}
}

// TestFetchRef_ZeroValueReturnsErrNotFound pins FetchRef's own guard: a
// Ref with nothing to expand returns ErrNotFound rather than dereferencing
// a nil RelationRef or issuing a host call for an empty ID.
func TestFetchRef_ZeroValueReturnsErrNotFound(t *testing.T) {
	var r Ref[valuesTestModel]

	if _, err := FetchRef[valuesTestModel](r); !IsNotFound(err) {
		t.Errorf("FetchRef on a zero Ref = %v, want ErrNotFound", err)
	}
}

// compileFetchRefAgainstRealModel proves FetchRef type-checks against
// valuesTestModel (Model + scanner, standing in for a real goerp module
// generate struct) — never called; the real host round trip panics
// outside a wasip1 build (imports_stub.go).
var _ func(Ref[valuesTestModel], ...AnyField[valuesTestModel]) (valuesTestModel, error) = FetchRef[valuesTestModel]

// compileRefAgainstMarkerType proves Ref[T] itself — unlike FetchRef —
// compiles fine against a bare Model with no Scan method, the same shape
// a cross-module marker type has (goerp#979): Ref only ever needs T to
// satisfy Model, never scanner.
var _ = Ref[crossModuleMarkerStub]{}

// Ref[T]'s compile-time guarantees can't be exercised as runtime tests,
// since a program that violates them doesn't build at all. Documented
// examples instead:
//
// A Ref built against one model's target isn't assignable to a field
// typed for another (goerp#979's own AC) — the same cross-model
// distinction issue #973 draws for Condition:
//
//	var custRef Ref[valuesTestModel]
//	var otherRef Ref[crossModuleMarkerStub]
//	custRef = otherRef // compile error: cannot use otherRef
//	                    // (value of type Ref[crossModuleMarkerStub]) as
//	                    // Ref[valuesTestModel] value in assignment
//
// FetchRef rejects a cross-module marker type outright — it carries no
// column data for a Scan method to populate, so it never satisfies
// ptrScanner:
//
//	FetchRef[crossModuleMarkerStub](Ref[crossModuleMarkerStub]{}) // compile error:
//	                    // *crossModuleMarkerStub does not implement
//	                    // scanner (missing method Scan)
