package db

import (
	"fmt"
	"strings"
)

// QueryBuilder fluently assembles a parameterized SELECT for dynamic
// filter sets that don't fit orm's model-level CRUD.
type QueryBuilder struct {
	table       string
	cols        []string
	conds       []string
	args        []any
	orderBy     string
	tiebreakCol string
	limit       int
	paginated   bool
}

// NewQuery starts a QueryBuilder against table.
func NewQuery(table string) *QueryBuilder {
	return &QueryBuilder{table: table}
}

// Select sets the SELECT column list; unset, SQL() selects "*".
func (qb *QueryBuilder) Select(cols ...string) *QueryBuilder {
	qb.cols = cols
	return qb
}

// Where adds a WHERE fragment, ANDed with every other Where/WhereIf call.
func (qb *QueryBuilder) Where(cond string) *QueryBuilder {
	qb.conds = append(qb.conds, cond)
	return qb
}

// WhereIf adds clause, ANDed in like Where, only when cond is true;
// clause's own "$?" placeholders are renumbered to real positional $n
// arguments by SQL(), skipping any WhereIf call whose cond was false.
func (qb *QueryBuilder) WhereIf(cond bool, clause string, args ...any) *QueryBuilder {
	if !cond {
		return qb
	}
	qb.conds = append(qb.conds, clause)
	qb.args = append(qb.args, args...)
	return qb
}

// OrderBy sets the ORDER BY clause body (e.g. "created_at DESC").
func (qb *QueryBuilder) OrderBy(clause string) *QueryBuilder {
	qb.orderBy = clause
	return qb
}

// Limit sets a row cap, baked into SQL() as a literal "LIMIT n" — unless
// Cursor is also called, in which case QueryPaged owns LIMIT/OFFSET and
// SQL() omits it to avoid emitting the clause twice.
func (qb *QueryBuilder) Limit(n int) *QueryBuilder {
	qb.limit = n
	return qb
}

// Cursor marks this query as QueryPaged-driven pagination and appends
// tiebreakCol to ORDER BY (if not already present) so ties in sortCol
// don't skip or repeat rows across pages. cursor and sortCol are accepted
// for call-site symmetry with QueryPaged's own (sql, cursor, limit,
// params) arguments, which a caller passes straight through itself.
func (qb *QueryBuilder) Cursor(cursor, sortCol, tiebreakCol string) *QueryBuilder {
	qb.paginated = true
	qb.tiebreakCol = tiebreakCol
	return qb
}

// SQL returns the assembled SELECT statement, with every WhereIf clause's
// own "$?" placeholders renumbered to contiguous positional $n arguments
// matching Args()'s order.
func (qb *QueryBuilder) SQL() string {
	var b strings.Builder
	b.WriteString("SELECT ")
	if len(qb.cols) == 0 {
		b.WriteString("*")
	} else {
		b.WriteString(strings.Join(qb.cols, ", "))
	}
	fmt.Fprintf(&b, " FROM %s", qb.table)

	if len(qb.conds) > 0 {
		b.WriteString(" WHERE ")
		b.WriteString(renumberPlaceholders(strings.Join(qb.conds, " AND ")))
	}

	orderBy := qb.orderBy
	if qb.tiebreakCol != "" && !strings.Contains(orderBy, qb.tiebreakCol) {
		if orderBy == "" {
			orderBy = qb.tiebreakCol
		} else {
			orderBy += ", " + qb.tiebreakCol
		}
	}
	if orderBy != "" {
		b.WriteString(" ORDER BY ")
		b.WriteString(orderBy)
	}

	if qb.limit > 0 && !qb.paginated {
		fmt.Fprintf(&b, " LIMIT %d", qb.limit)
	}

	return b.String()
}

// Args returns the positional arguments SQL()'s own "$?"-derived
// placeholders bind to, in order.
func (qb *QueryBuilder) Args() []any {
	return qb.args
}

// renumberPlaceholders replaces each "$?" in cond, left to right, with
// $1, $2, ... .
func renumberPlaceholders(cond string) string {
	var b strings.Builder
	n := 0
	for {
		i := strings.Index(cond, "$?")
		if i < 0 {
			b.WriteString(cond)
			return b.String()
		}
		n++
		b.WriteString(cond[:i])
		fmt.Fprintf(&b, "$%d", n)
		cond = cond[i+2:]
	}
}
