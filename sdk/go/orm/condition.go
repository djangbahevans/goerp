package orm

// Condition is a typed, composable filter for TModel that compiles to a
// domain expression string. Build one with the Field comparison methods,
// or with Raw or MatchAll.
type Condition[TModel Model] struct {
	expr string
}

// And combines c and other with AND, parenthesizing both sides so an
// Or-built operand keeps its grouping: person.Or(company).And(active)
// means "(person OR company) AND active".
func (c Condition[TModel]) And(other Condition[TModel]) Condition[TModel] {
	return Condition[TModel]{expr: "(" + c.expr + ") AND (" + other.expr + ")"}
}

// Or combines c and other with OR, parenthesizing both sides so the result
// keeps its grouping when combined further.
func (c Condition[TModel]) Or(other Condition[TModel]) Condition[TModel] {
	return Condition[TModel]{expr: "(" + c.expr + ") OR (" + other.expr + ")"}
}

// Not negates c with the domain language's NOT operator.
func (c Condition[TModel]) Not() Condition[TModel] {
	return Condition[TModel]{expr: "NOT (" + c.expr + ")"}
}

// Raw wraps a domain expression the typed builder cannot express, such as
// child_of/parent_of or a Transient or Virtual model's interpreted fields.
// It skips field-name and value-type checking. Escape embedded values with
// Domain, and parenthesize domain if it mixes AND/OR and will be combined
// further.
func Raw[TModel Model](domain string) Condition[TModel] {
	return Condition[TModel]{expr: domain}
}

// MatchAll is an always-true filter, used to obtain a BoundCondition that
// deliberately touches every record.
func MatchAll[TModel Model]() Condition[TModel] {
	return Condition[TModel]{expr: "true"}
}

// BoundCondition is a Condition explicitly acknowledged as a bulk-write
// filter. WriteWhere accepts only this type, so a forgotten filter cannot
// silently touch every row.
type BoundCondition[TModel Model] struct {
	Condition[TModel]
}

// Bind acknowledges c as an intentional bulk-write filter.
func (c Condition[TModel]) Bind() BoundCondition[TModel] {
	return BoundCondition[TModel]{Condition: c}
}

// Unbounded is MatchAll().Bind(): every record, stated explicitly at a
// WriteWhere call site.
func Unbounded[TModel Model]() BoundCondition[TModel] {
	return MatchAll[TModel]().Bind()
}
