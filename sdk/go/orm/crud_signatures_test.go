package orm

// This file's only job is to make Create/CreateBatch/Write/WriteMany/
// WriteWhere/FirstOrCreate/Unlink (and their _Tx variants) compile
// against valuesTestModel — a hand-written type satisfying Model +
// scanner, standing in for what goerp module generate (issue #977)
// emits onto a real model struct. Calling these would panic outside a
// wasip1 build (imports_stub.go) — proving they type-check, with PT
// inferred as *valuesTestModel via ptrScanner[T]'s own pointer-method
// constraint, not compile-time.
var (
	_ func(*Values[valuesTestModel], ...CreateOption) (valuesTestModel, error)     = Create[valuesTestModel]
	_                                                                              = CreateTx[valuesTestModel]
	_ func([]*Values[valuesTestModel], ...CreateOption) ([]valuesTestModel, error) = CreateBatch[valuesTestModel]
	_                                                                              = CreateBatchTx[valuesTestModel]

	_ func(string, *Values[valuesTestModel], *string) error                               = Write[valuesTestModel]
	_                                                                                     = WriteTx[valuesTestModel]
	_ func([]string, *Values[valuesTestModel]) (ExecResult, error)                        = WriteMany[valuesTestModel]
	_                                                                                     = WriteManyTx[valuesTestModel]
	_ func(BoundCondition[valuesTestModel], *Values[valuesTestModel]) (ExecResult, error) = WriteWhere[valuesTestModel]
	_                                                                                     = WriteWhereTx[valuesTestModel]

	_ func(*Values[valuesTestModel], *Values[valuesTestModel]) (valuesTestModel, bool, error) = FirstOrCreate[valuesTestModel]
	_                                                                                         = FirstOrCreateTx[valuesTestModel]

	_ func(...string) (ExecResult, error) = Unlink[valuesTestModel]
	_                                     = UnlinkTx[valuesTestModel]
)

// WriteWhere rejects a bare Condition[T] at compile time — only
// BoundCondition[T] is accepted, so a forgotten filter can't silently
// touch every row. Can't be exercised as a runtime test, since a program
// that violates it doesn't build at all:
//
//	var c Condition[valuesTestModel]
//	WriteWhere(c, NewValues[valuesTestModel]())          // compile error:
//	                                                      // Condition[valuesTestModel]
//	                                                      // is not BoundCondition[valuesTestModel]
//	WriteWhere(c.Bind(), NewValues[valuesTestModel]())   // fine
//	WriteWhere(Unbounded[valuesTestModel](), NewValues[valuesTestModel]()) // fine — every record, explicitly
