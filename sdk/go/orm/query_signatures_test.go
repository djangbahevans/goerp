package orm

// This file's only job is to make Get/GetMany/Count/Sum/Avg/Min/Max/
// Mutate (and their _Tx variants) plus Query[T]'s own methods compile
// against valuesTestModel — a hand-written type satisfying Model +
// scanner, standing in for what goerp module generate (issue #977)
// emits onto a real model struct. Calling these would panic outside a
// wasip1 build (imports_stub.go) — proving they type-check, not that
// they round-trip.
var (
	_ func(string, ...AnyField[valuesTestModel]) (valuesTestModel, error)     = Get[valuesTestModel]
	_                                                                         = GetTx[valuesTestModel]
	_ func([]string, ...AnyField[valuesTestModel]) ([]valuesTestModel, error) = GetMany[valuesTestModel]
	_                                                                         = GetManyTx[valuesTestModel]

	_ func(Condition[valuesTestModel]) (int64, error) = Count[valuesTestModel]

	_ func(string, ...MutateOption[valuesTestModel]) (valuesTestModel, error) = Mutate[valuesTestModel]
	_                                                                         = MutateTx[valuesTestModel]

	_ = From[valuesTestModel]

	valuesTestModelPrice = NewOrderedField[valuesTestModel, int64]("price")
)

// compileAggregatesAndMutateOptions proves Sum/Avg/Min/Max/Increment/
// Decrement type-check against valuesTestModelPrice, an OrderedField
// over a Numeric TValue. Never called — the real host round trip panics
// outside a wasip1 build (imports_stub.go).
func compileAggregatesAndMutateOptions() {
	_, _ = Sum(valuesTestModelPrice, MatchAll[valuesTestModel]())
	_, _ = Avg(valuesTestModelPrice, MatchAll[valuesTestModel]())
	_, _ = Min(valuesTestModelPrice, MatchAll[valuesTestModel]())
	_, _ = Max(valuesTestModelPrice, MatchAll[valuesTestModel]())
	_ = Increment(valuesTestModelPrice.Field, int64(1))
	_ = Decrement(valuesTestModelPrice.Field, int64(1))
}

var _ = compileAggregatesAndMutateOptions

// valuesTestModelStatusT stands in for a generated Selection/Enum's own
// named string type — satisfies Ordered (OrderedField accepts it) but
// not Numeric (Sum/Avg reject it), the same distinction goerp#977's real
// generated types draw.
type valuesTestModelStatusT string

// Sum/Avg reject a non-OrderedField argument, and an OrderedField whose
// TValue satisfies the broader Ordered constraint but not the stricter
// Numeric one — neither can be exercised as a runtime test, since a
// program that violates either doesn't build at all:
//
//	name := NewField[valuesTestModel, string]("name")
//	_, _ = Sum(name, MatchAll[valuesTestModel]()) // compile error: Field
//	                          // is not OrderedField[valuesTestModel, TValue]
//
//	status := NewOrderedField[valuesTestModel, valuesTestModelStatusT]("status")
//	_, _ = Sum(status, MatchAll[valuesTestModel]()) // compile error:
//	                          // valuesTestModelStatusT does not implement
//	                          // Numeric (string missing in its type set)
//
// Increment/Decrement likewise reject a delta whose Go type doesn't
// match f's own declared type:
//
//	_ = Increment(valuesTestModelPrice.Field, int32(1)) // compile error:
//	                          // int32 does not match valuesTestModelPrice's
//	                          // declared int64
var _ = valuesTestModelStatusT("")
