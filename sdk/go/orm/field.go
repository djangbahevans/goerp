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

// Name returns the field's bare column name, the form Select and Mutate's
// Increment/Decrement take.
func (f Field[TModel, TValue]) Name() string { return f.name }

// ref returns "record.{name}", the only form of a field name a domain
// expression accepts.
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

// Ordered is every Go type a generated field can have except time.Time,
// which uses TimeField.
type Ordered interface {
	~int32 | ~int64 | ~float64 | ~string
}

// OrderedField adds range comparisons. It includes Decimal and Selection or
// Enum string types: the database compares the column's own type, and the
// Go type only controls how the literal is written.
type OrderedField[TModel Model, TValue Ordered] struct {
	Field[TModel, TValue]
}

// NewOrderedField declares an OrderedField named name on TModel.
func NewOrderedField[TModel Model, TValue Ordered](name string) OrderedField[TModel, TValue] {
	return OrderedField[TModel, TValue]{Field: NewField[TModel, TValue](name)}
}

// isSortable restricts Min and Max to OrderedField and TimeField.
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

// Between builds an inclusive-range Condition, compiled to
// ">= lo AND <= hi".
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

// isSortable restricts Min and Max to OrderedField and TimeField.
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

// Numeric is the value type Sum and Avg accept: Integer, BigInt and Float
// fields, plus Decimal's plain string. The string arm has no "~", so
// Selection and Enum types, which are defined string types, are excluded.
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
