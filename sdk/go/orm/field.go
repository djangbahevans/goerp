package orm

import (
	"strings"
	"time"
)

// Field is a typed reference to one column: TModel binds it to its
// owning model (so it can't build a Condition[TOther]), TValue binds it
// to the column's own Go type (so Eq/In/Set can't be called with a
// mismatched type).
type Field[TModel Model, TValue any] struct {
	name string
}

// NewField declares a Field named name on TModel.
func NewField[TModel Model, TValue any](name string) Field[TModel, TValue] {
	return Field[TModel, TValue]{name: name}
}

// Name returns the field's bare column name — the form a fields list
// (Select, Mutate's Increment/Decrement) takes. A compiled domain
// expression (manifest-spec.md §8) needs the qualified "record.{name}"
// form instead, which ref (below) — not Name — supplies.
func (f Field[TModel, TValue]) Name() string { return f.name }

// ref returns the field's domain-qualified reference — the only valid
// form of a field name inside a compiled domain expression
// (internal/engine/domain's parser accepts "record.{name}", "current_
// user.{attr}" and a handful of keywords; a bare identifier like the
// field's own Name() is rejected outright as "unknown identifier").
func (f Field[TModel, TValue]) ref() string { return "record." + f.name }

// Eq builds an equality Condition against v.
func (f Field[TModel, TValue]) Eq(v TValue) Condition[TModel] {
	return Condition[TModel]{expr: f.ref() + " = " + domainLiteral(v)}
}

// Neq builds an inequality Condition against v.
func (f Field[TModel, TValue]) Neq(v TValue) Condition[TModel] {
	return Condition[TModel]{expr: f.ref() + " != " + domainLiteral(v)}
}

// In builds a set-membership Condition. An empty vs is always false —
// "IN ()" isn't valid domain-grammar syntax, and vacuously matching
// nothing is the only sound reading of "in an empty set".
func (f Field[TModel, TValue]) In(vs ...TValue) Condition[TModel] {
	if len(vs) == 0 {
		return Condition[TModel]{expr: "false"}
	}
	return Condition[TModel]{expr: f.ref() + " IN (" + joinLiterals(vs) + ")"}
}

// NotIn builds the negation of In. An empty vs is always true, matching
// In's empty-set handling.
func (f Field[TModel, TValue]) NotIn(vs ...TValue) Condition[TModel] {
	if len(vs) == 0 {
		return Condition[TModel]{expr: "true"}
	}
	return Condition[TModel]{expr: "NOT " + f.ref() + " IN (" + joinLiterals(vs) + ")"}
}

// IsNull builds a null-check Condition.
func (f Field[TModel, TValue]) IsNull() Condition[TModel] {
	return Condition[TModel]{expr: f.ref() + " IS NULL"}
}

// IsNotNull builds a not-null-check Condition.
func (f Field[TModel, TValue]) IsNotNull() Condition[TModel] {
	return Condition[TModel]{expr: f.ref() + " IS NOT NULL"}
}

func joinLiterals[TValue any](vs []TValue) string {
	lits := make([]string, len(vs))
	for i, v := range vs {
		lits[i] = domainLiteral(v)
	}
	return strings.Join(lits, ", ")
}

// StringField adds pattern matching — Char/Text fields only. UUID,
// Sequence, Decimal, and Time fields are also Go strings but pattern
// matching isn't meaningful on them, so those stay plain
// Field[TModel, string].
type StringField[TModel Model] struct {
	Field[TModel, string]
}

// NewStringField declares a StringField named name on TModel.
func NewStringField[TModel Model](name string) StringField[TModel] {
	return StringField[TModel]{Field: NewField[TModel, string](name)}
}

// Like builds a case-sensitive pattern-match Condition.
func (f StringField[TModel]) Like(pattern string) Condition[TModel] {
	return Condition[TModel]{expr: f.ref() + " LIKE " + domainLiteral(pattern)}
}

// ILike builds a case-insensitive pattern-match Condition.
func (f StringField[TModel]) ILike(pattern string) Condition[TModel] {
	return Condition[TModel]{expr: f.ref() + " ILIKE " + domainLiteral(pattern)}
}

// Ordered is every base Go type baseGoType (internal/module/generate_fields.go)
// maps a FieldKind onto except time.Time (a struct, gets TimeField
// instead).
type Ordered interface {
	~int32 | ~int64 | ~float64 | ~string
}

// OrderedField adds range comparisons. Includes Decimal and
// Selection/Enum's named string types (~string) — the server compares
// the real Postgres column, not the Go string; the Go type only
// controls literal serialization (domainLiteral's existing per-type
// rule).
type OrderedField[TModel Model, TValue Ordered] struct {
	Field[TModel, TValue]
}

// NewOrderedField declares an OrderedField named name on TModel.
func NewOrderedField[TModel Model, TValue Ordered](name string) OrderedField[TModel, TValue] {
	return OrderedField[TModel, TValue]{Field: NewField[TModel, TValue](name)}
}

// isSortable satisfies Sortable (aggregate.go) — Min/Max's own
// constraint, restricting them to OrderedField/TimeField specifically
// rather than any AnyField (a BytesField or a plain boolean Field has no
// meaningful minimum/maximum).
func (OrderedField[TModel, TValue]) isSortable() {}

// Gt builds a greater-than Condition.
func (f OrderedField[TModel, TValue]) Gt(v TValue) Condition[TModel] {
	return Condition[TModel]{expr: f.ref() + " > " + domainLiteral(v)}
}

// Gte builds a greater-than-or-equal Condition.
func (f OrderedField[TModel, TValue]) Gte(v TValue) Condition[TModel] {
	return Condition[TModel]{expr: f.ref() + " >= " + domainLiteral(v)}
}

// Lt builds a less-than Condition.
func (f OrderedField[TModel, TValue]) Lt(v TValue) Condition[TModel] {
	return Condition[TModel]{expr: f.ref() + " < " + domainLiteral(v)}
}

// Lte builds a less-than-or-equal Condition.
func (f OrderedField[TModel, TValue]) Lte(v TValue) Condition[TModel] {
	return Condition[TModel]{expr: f.ref() + " <= " + domainLiteral(v)}
}

// Between builds an inclusive-range Condition — the domain grammar
// (manifest-spec.md §8) has no BETWEEN token, so this compiles to the
// equivalent ">= lo AND <= hi", which AND's own precedence (tighter than
// OR, same as SQL) keeps grouped correctly wherever it's combined.
func (f OrderedField[TModel, TValue]) Between(lo, hi TValue) Condition[TModel] {
	return Condition[TModel]{expr: f.ref() + " >= " + domainLiteral(lo) + " AND " + f.ref() + " <= " + domainLiteral(hi)}
}

// TimeField covers TimestampTZ and Date.
type TimeField[TModel Model] struct {
	Field[TModel, time.Time]
}

// NewTimeField declares a TimeField named name on TModel.
func NewTimeField[TModel Model](name string) TimeField[TModel] {
	return TimeField[TModel]{Field: NewField[TModel, time.Time](name)}
}

// isSortable satisfies Sortable (aggregate.go) — see OrderedField's own
// isSortable for why.
func (TimeField[TModel]) isSortable() {}

// Before builds a less-than Condition against t.
func (f TimeField[TModel]) Before(t time.Time) Condition[TModel] {
	return Condition[TModel]{expr: f.ref() + " < " + domainLiteral(t)}
}

// After builds a greater-than Condition against t.
func (f TimeField[TModel]) After(t time.Time) Condition[TModel] {
	return Condition[TModel]{expr: f.ref() + " > " + domainLiteral(t)}
}

// Between builds an inclusive-range Condition, the same ">= AND <="
// rendering as OrderedField.Between.
func (f TimeField[TModel]) Between(from, to time.Time) Condition[TModel] {
	return Condition[TModel]{expr: f.ref() + " >= " + domainLiteral(from) + " AND " + f.ref() + " <= " + domainLiteral(to)}
}

// Numeric is the TValue OrderedField needs to additionally satisfy for
// Sum/Avg (issue #976) — the concrete int32/int64/float64 Integer/
// BigInt/Float fields generate as, plus the bare string Decimal
// serializes as. Deliberately no "~" on the string arm: a defined type
// whose underlying type is string (Selection/Enum's generated named
// types) does not satisfy this, even though it does satisfy the broader
// Ordered constraint OrderedField itself requires — summing an enum
// value is meaningless, summing a decimal isn't. Sum/Avg are themselves
// generic over TValue Numeric (aggregate.go) rather than requiring an
// adapter type here — Go checks the constraint at the Sum/Avg call site
// directly, so an OrderedField stays usable for Where-filtering and
// Sum/Avg both, with no wrap/unwrap step at the call site.
type Numeric interface {
	~int32 | ~int64 | ~float64 | string
}

// BytesField covers JSONB and Bytea. Does not embed Field — no
// Eq/Neq/In: exact-byte equality is rarely meaningful and for JSONB
// specifically would be wrong (Postgres JSONB equality isn't byte
// equality). Only null checks are exposed; anything else is Raw.
type BytesField[TModel Model] struct {
	name string
}

// NewBytesField declares a BytesField named name on TModel.
func NewBytesField[TModel Model](name string) BytesField[TModel] {
	return BytesField[TModel]{name: name}
}

// Name returns the field's bare column name.
func (f BytesField[TModel]) Name() string { return f.name }

// IsNull builds a null-check Condition.
func (f BytesField[TModel]) IsNull() Condition[TModel] {
	return Condition[TModel]{expr: "record." + f.name + " IS NULL"}
}

// IsNotNull builds a not-null-check Condition.
func (f BytesField[TModel]) IsNotNull() Condition[TModel] {
	return Condition[TModel]{expr: "record." + f.name + " IS NOT NULL"}
}
