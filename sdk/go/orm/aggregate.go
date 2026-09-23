package orm

import (
	"fmt"

	abi "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/djangbahevans/goerp/sdk/go/internal/hostcall"
)

// Count returns the number of records matching cond via
// host.orm.aggregate, under the same row-level security a Query
// applies — a record hidden by RLS is excluded the same way it would be
// from a Query result.
func Count[T Model](cond Condition[T]) (int64, error) {
	v, err := aggregateOne(cond, abi.ORMAggregateValue{Aggregation: "count"})
	if err != nil {
		return 0, err
	}
	return toInt64(v)
}

// Sum totals f over the records matching cond via host.orm.aggregate, 0
// when no record matches. f's TValue must additionally satisfy Numeric
// (field.go) — an OrderedField over a Selection/Enum named string type,
// or any non-OrderedField, fails to compile.
func Sum[T Model, TValue Numeric](f OrderedField[T, TValue], cond Condition[T]) (float64, error) {
	return numericAggregate(f.Name(), cond, "sum")
}

// Avg returns the average value of f over the records matching cond via
// host.orm.aggregate, 0 when no record matches.
func Avg[T Model, TValue Numeric](f OrderedField[T, TValue], cond Condition[T]) (float64, error) {
	return numericAggregate(f.Name(), cond, "avg")
}

// Sortable is OrderedField+TimeField — anything Min/Max can meaningfully
// range over, broader than Sum/Avg's Numeric requirement (Min over a
// Char or TimestampTZ field is meaningful; summing/averaging either is
// not). Implemented via an unexported marker method both OrderedField
// and TimeField carry directly (field.go) — unlike Sum/Avg's Numeric
// constraint, TValue Ordered's own type set can't distinguish "any
// OrderedField" from "just the ones that should be Sortable" (it's meant
// to include every OrderedField already), and TimeField isn't an
// OrderedField at all, so a marker method is the only way to admit both
// concrete types while excluding BytesField and a plain Field[T, bool].
type Sortable[TModel Model] interface {
	AnyField[TModel]
	isSortable()
}

// Min returns the smallest value of f over the records matching cond via
// host.orm.aggregate, 0 when no record matches.
func Min[T Model](f Sortable[T], cond Condition[T]) (float64, error) {
	return numericAggregate(f.Name(), cond, "min")
}

// Max returns the largest value of f over the records matching cond via
// host.orm.aggregate, 0 when no record matches.
func Max[T Model](f Sortable[T], cond Condition[T]) (float64, error) {
	return numericAggregate(f.Name(), cond, "max")
}

func numericAggregate[T Model](field string, cond Condition[T], aggregation string) (float64, error) {
	v, err := aggregateOne(cond, abi.ORMAggregateValue{Field: field, Aggregation: aggregation})
	if err != nil {
		return 0, err
	}
	return toFloat64(v)
}

func aggregateOne[T Model](cond Condition[T], value abi.ORMAggregateValue) (any, error) {
	var out abi.ORMAggregateOutput
	in := abi.ORMAggregateInput{Model: resourceName[T](), Domain: cond.expr, Values: []abi.ORMAggregateValue{value}}
	if err := hostcall.Do(hostORMAggregate, in, &out); err != nil {
		return nil, err
	}
	alias := value.Field + "_" + value.Aggregation
	return out.Values[alias], nil
}

func toInt64(v any) (int64, error) {
	switch n := v.(type) {
	case nil:
		return 0, nil
	case int64:
		return n, nil
	case int:
		return int64(n), nil
	case float64:
		return int64(n), nil
	case string:
		var i int64
		if _, err := fmt.Sscanf(n, "%d", &i); err != nil {
			return 0, fmt.Errorf("orm: cannot parse count %q as an integer: %w", n, err)
		}
		return i, nil
	default:
		return 0, fmt.Errorf("orm: unexpected count value type %T", v)
	}
}

func toFloat64(v any) (float64, error) {
	switch n := v.(type) {
	case nil:
		return 0, nil
	case float64:
		return n, nil
	case float32:
		return float64(n), nil
	case int64:
		return float64(n), nil
	case int:
		return float64(n), nil
	case string:
		var f float64
		if _, err := fmt.Sscanf(n, "%g", &f); err != nil {
			return 0, fmt.Errorf("orm: cannot parse aggregate value %q as a number: %w", n, err)
		}
		return f, nil
	default:
		return 0, fmt.Errorf("orm: unexpected aggregate value type %T", v)
	}
}
