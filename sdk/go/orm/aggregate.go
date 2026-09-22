package orm

import (
	"fmt"

	abi "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/djangbahevans/goerp/sdk/go/db"
	"github.com/djangbahevans/goerp/sdk/go/internal/hostcall"
)

// Count returns the number of records matching domain via
// host.orm.aggregate, under the same row-level security Search applies —
// a record hidden by RLS is excluded the same way it would be from a
// Search result.
func Count(model, domain string) (int64, error) {
	return count("", model, domain)
}

// CountTx is Count, scoped to tx's own open transaction.
func CountTx(tx *db.Tx, model, domain string) (int64, error) {
	return count(tx.TxID(), model, domain)
}

func count(txID, model, domain string) (int64, error) {
	v, err := aggregateOne(txID, model, domain, abi.ORMAggregateValue{Aggregation: "count"})
	if err != nil {
		return 0, err
	}
	return toInt64(v)
}

// Sum totals field over the records matching domain via
// host.orm.aggregate, 0 when no record matches. field must be a numeric
// field, or the call fails with orm.validation_failed (check via
// IsValidationFailed).
func Sum(model, field, domain string) (float64, error) {
	return numericAggregate("", model, field, domain, "sum")
}

// SumTx is Sum, scoped to tx's own open transaction.
func SumTx(tx *db.Tx, model, field, domain string) (float64, error) {
	return numericAggregate(tx.TxID(), model, field, domain, "sum")
}

// Min returns the smallest value of field over the records matching
// domain via host.orm.aggregate, 0 when no record matches.
func Min(model, field, domain string) (float64, error) {
	return numericAggregate("", model, field, domain, "min")
}

// MinTx is Min, scoped to tx's own open transaction.
func MinTx(tx *db.Tx, model, field, domain string) (float64, error) {
	return numericAggregate(tx.TxID(), model, field, domain, "min")
}

// Max returns the largest value of field over the records matching
// domain via host.orm.aggregate, 0 when no record matches.
func Max(model, field, domain string) (float64, error) {
	return numericAggregate("", model, field, domain, "max")
}

// MaxTx is Max, scoped to tx's own open transaction.
func MaxTx(tx *db.Tx, model, field, domain string) (float64, error) {
	return numericAggregate(tx.TxID(), model, field, domain, "max")
}

// Avg returns the average value of field over the records matching
// domain via host.orm.aggregate, 0 when no record matches.
func Avg(model, field, domain string) (float64, error) {
	return numericAggregate("", model, field, domain, "avg")
}

// AvgTx is Avg, scoped to tx's own open transaction.
func AvgTx(tx *db.Tx, model, field, domain string) (float64, error) {
	return numericAggregate(tx.TxID(), model, field, domain, "avg")
}

func numericAggregate(txID, model, field, domain, aggregation string) (float64, error) {
	v, err := aggregateOne(txID, model, domain, abi.ORMAggregateValue{Field: field, Aggregation: aggregation})
	if err != nil {
		return 0, err
	}
	return toFloat64(v)
}

func aggregateOne(txID, model, domain string, value abi.ORMAggregateValue) (any, error) {
	var out abi.ORMAggregateOutput
	in := abi.ORMAggregateInput{Model: model, Domain: domain, Values: []abi.ORMAggregateValue{value}, TxID: txID}
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
