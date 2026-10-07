package domain

import (
	"maps"
	"slices"
)

// RecordFields returns the sorted, de-duplicated names of the record fields
// expr reads, so a caller fetching a record to evaluate it selects only
// those columns.
func RecordFields(expr Expr) []string {
	seen := make(map[string]struct{})
	collectRecordFields(expr, seen)
	return slices.Sorted(maps.Keys(seen))
}

func collectRecordFields(expr Expr, seen map[string]struct{}) {
	switch e := expr.(type) {
	case RecordField:
		if e.Field != "" {
			seen[e.Field] = struct{}{}
		}
	case BinaryExpr:
		collectRecordFields(e.Left, seen)
		collectRecordFields(e.Right, seen)
	case UnaryExpr:
		collectRecordFields(e.Operand, seen)
	case IsNullExpr:
		collectRecordFields(e.Operand, seen)
	case InExpr:
		collectRecordFields(e.Operand, seen)
		for _, v := range e.Values {
			collectRecordFields(v, seen)
		}
	case TreeExpr:
		collectRecordFields(e.Target, seen)
	}
}
