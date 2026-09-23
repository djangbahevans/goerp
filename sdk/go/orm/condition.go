package orm

// Condition is a typed, composable filter for TModel, compiling to
// exactly the domain expression language (manifest-spec.md §8) string
// host.orm already accepts. The Field comparison methods (field.go) are
// the only way to build one besides the two escape hatches below.
type Condition[TModel Model] struct {
	expr string
}

// And combines c and other with the domain language's AND operator. AND
// binds tighter than OR in the domain grammar (manifest-spec.md §8, same
// as SQL), so a chain of And calls never needs parentheses to keep its
// intended grouping.
func (c Condition[TModel]) And(other Condition[TModel]) Condition[TModel] {
	return Condition[TModel]{expr: c.expr + " AND " + other.expr}
}

// Or combines c and other with the domain language's OR operator,
// parenthesizing both sides so the result groups correctly regardless of
// what it's later combined with — OR binds loosest, so an unparenthesized
// operand could be silently absorbed into a surrounding AND.
func (c Condition[TModel]) Or(other Condition[TModel]) Condition[TModel] {
	return Condition[TModel]{expr: "(" + c.expr + ") OR (" + other.expr + ")"}
}

// Not negates c with the domain language's NOT operator.
func Not[TModel Model](c Condition[TModel]) Condition[TModel] {
	return Condition[TModel]{expr: "NOT (" + c.expr + ")"}
}

// Raw is the escape hatch for a domain expression the typed builder can't
// express yet (child_of/parent_of, a Transient/Virtual backend's
// interpreted fields, etc.) — bypasses field-name/value-type checking,
// the same way orm.Domain already does. The caller is responsible for
// escaping any embedded value (orm.Domain does this) and for
// parenthesizing domain if it mixes AND/OR and will be combined further.
func Raw[TModel Model](domain string) Condition[TModel] {
	return Condition[TModel]{expr: domain}
}

// MatchAll is the only zero-argument Condition constructor — an
// always-true filter, used to obtain a BoundCondition (issue #975) that
// deliberately touches every record.
func MatchAll[TModel Model]() Condition[TModel] {
	return Condition[TModel]{expr: "true"}
}

// BoundCondition is the only type WriteWhere (issue #975) accepts — a
// Condition explicitly acknowledged as the actual bulk-write filter, so a
// forgotten filter argument can't silently touch every row.
type BoundCondition[TModel Model] struct {
	Condition[TModel]
}

// Bind acknowledges c as an intentional bulk-write filter.
func (c Condition[TModel]) Bind() BoundCondition[TModel] {
	return BoundCondition[TModel]{Condition: c}
}

// Unbounded is Bind() on MatchAll — an explicit, self-documenting way to
// say "every record" at a WriteWhere call site.
func Unbounded[TModel Model]() BoundCondition[TModel] {
	return MatchAll[TModel]().Bind()
}
