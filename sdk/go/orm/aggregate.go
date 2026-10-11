package orm

import (
	"fmt"

	abi "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/djangbahevans/goerp/sdk/go/internal/hostcall"
)

// Count returns the number of matching records visible under row-level security.
func Count[T Model](cond Condition[T]) (int64, error) {
	v, err := cond.aggregateOne(abi.ORMAggregateValue{Aggregation: "count"})
	if err != nil {
		return 0, err
	}

	return toInt64(v)
}

// Sum totals a numeric field over visible matching records, returning zero
// when no non-null values match.
func Sum[T Model, TValue Numeric](f OrderedField[T, TValue], cond Condition[T]) (float64, error) {
	return cond.numericAggregate(f.Name(), "sum")
}

// Avg averages a numeric field over visible matching records, returning zero
// when no non-null values match.
func Avg[T Model, TValue Numeric](f OrderedField[T, TValue], cond Condition[T]) (float64, error) {
	return cond.numericAggregate(f.Name(), "avg")
}

// Sortable admits OrderedField, StringField and TimeField while preserving
// the field's value type. Boolean, relation and binary fields are excluded.
type Sortable[TModel Model, TValue any] interface {
	AnyField[TModel]
	isSortable(TModel, TValue)
}

// Min returns the smallest non-null value visible under row-level security.
// The result retains the field's Go type and is nil when no non-null values
// match. A field without read permission returns a field-read-denied error.
func Min[T Model, TValue any](f Sortable[T, TValue], cond Condition[T]) (*TValue, error) {
	return cond.aggregateExtremum[TValue](f.Name(), "min")
}

// Max returns the largest non-null value visible under row-level security.
// The result retains the field's Go type and is nil when no non-null values
// match. A field without read permission returns a field-read-denied error.
func Max[T Model, TValue any](f Sortable[T, TValue], cond Condition[T]) (*TValue, error) {
	return cond.aggregateExtremum[TValue](f.Name(), "max")
}

func (cond Condition[T]) aggregateExtremum[TValue any](field, aggregation string) (*TValue, error) {
	in := abi.ORMAggregateInput{
		Model:  resourceName[T](),
		Domain: cond.expr,
		Values: []abi.ORMAggregateValue{{Field: field, Aggregation: aggregation}},
	}
	var out struct {
		Values map[string]*TValue `msgpack:"values"`
	}
	if err := hostcall.Do(hostORMAggregate, in, &out); err != nil {
		return nil, err
	}

	value, ok := out.Values[field+"_"+aggregation]
	if !ok {
		return nil, fmt.Errorf("orm: missing %s result for field %s", aggregation, field)
	}

	return value, nil
}

func (cond Condition[T]) numericAggregate(field, aggregation string) (float64, error) {
	v, err := cond.aggregateOne(abi.ORMAggregateValue{Field: field, Aggregation: aggregation})
	if err != nil {
		return 0, err
	}

	return toFloat64(v)
}

func (cond Condition[T]) aggregateOne(value abi.ORMAggregateValue) (any, error) {
	var out abi.ORMAggregateOutput
	in := abi.ORMAggregateInput{
		Model:  resourceName[T](),
		Domain: cond.expr,
		Values: []abi.ORMAggregateValue{value},
	}
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
