package orm

import (
	"fmt"

	abi "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/djangbahevans/goerp/sdk/go/db"
	"github.com/djangbahevans/goerp/sdk/go/internal/hostcall"
)

// Query is the typed replacement for Search/SearchRead's model/domain/
// fields string arguments — build one with From, narrow it with Where/
// Select/OrderBy/Limit/Cursor/Tx, then run it with All/One/IDs/Count.
type Query[T Model] struct {
	model      string
	cond       Condition[T]
	hasCond    bool
	fields     []AnyField[T]
	order      string
	limit      int
	cursor     string
	txID       string
	decodeMany func([]map[string]any) ([]T, error)
}

// From starts a Query for T, deriving its model name from T's own
// ResourceName() — no separate model string to mismatch against T.
func From[T Model, PT ptrScanner[T]]() *Query[T] {
	return &Query[T]{
		model:      resourceName[T](),
		decodeMany: decodeRecords[T, PT],
	}
}

// Where narrows q to records matching c, AND-combined with any prior
// Where on the same Query.
func (q *Query[T]) Where(c Condition[T]) *Query[T] {
	if !q.hasCond {
		q.cond = c
	} else {
		q.cond = q.cond.And(c)
	}
	q.hasCond = true
	return q
}

// Select limits which fields All/One populate — defaults to every field
// the caller's field-security context permits when never called.
func (q *Query[T]) Select(fields ...AnyField[T]) *Query[T] {
	q.fields = fields
	return q
}

// OrderBy orders results by f, ascending unless desc is set. A later
// call replaces an earlier one — Query orders by a single field, the
// same limit host.orm.search_read's own Order member has.
func (q *Query[T]) OrderBy(f AnyField[T], desc bool) *Query[T] {
	q.order = f.Name()
	if desc {
		q.order += " DESC"
	}
	return q
}

// Limit caps the number of results All/One/IDs return.
func (q *Query[T]) Limit(n int) *Query[T] {
	q.limit = n
	return q
}

// Cursor resumes All from a previous page's next cursor. IDs and Count
// reject a Query with Cursor set — host.orm.search has no next-cursor
// return to resume from, and silently ignoring it would leave a caller
// stuck on the same page forever with no signal anything was wrong.
func (q *Query[T]) Cursor(cursor string) *Query[T] {
	q.cursor = cursor
	return q
}

// Tx scopes All/One/IDs/Count to tx's own open transaction.
func (q *Query[T]) Tx(tx *db.Tx) *Query[T] {
	q.txID = tx.TxID()
	return q
}

func (q *Query[T]) domain() string {
	if !q.hasCond {
		return ""
	}
	return q.cond.expr
}

// All runs q via host.orm.search_read, mapping each result into a T.
// Returns (records, nextCursor, error); nextCursor is "" when there are
// no more pages.
func (q *Query[T]) All() ([]T, string, error) {
	var out abi.ORMSearchReadOutput
	in := abi.ORMSearchReadInput{
		Model: q.model, Domain: q.domain(), Fields: fieldNames(q.fields),
		Order: q.order, Limit: q.limit, Cursor: q.cursor, TxID: q.txID,
	}
	if err := hostcall.Do(hostORMSearchRead, in, &out); err != nil {
		return nil, "", err
	}
	records, err := q.decodeMany(out.Records)
	if err != nil {
		return nil, "", err
	}
	return records, out.NextCursor, nil
}

// One is Limit(1).All(), returning ErrNotFound if it matches nothing.
func (q *Query[T]) One() (T, error) {
	var zero T
	records, _, err := q.Limit(1).All()
	if err != nil {
		return zero, err
	}
	if len(records) == 0 {
		return zero, ErrNotFound
	}
	return records[0], nil
}

// IDs runs q via host.orm.search, returning matching IDs without
// fetching field data.
func (q *Query[T]) IDs() ([]string, error) {
	if q.cursor != "" {
		return nil, fmt.Errorf("orm: Query.IDs does not support Cursor (use All for cursor pagination)")
	}
	var out abi.ORMSearchOutput
	in := abi.ORMSearchInput{Model: q.model, Domain: q.domain(), Order: q.order, Limit: q.limit, TxID: q.txID}
	err := hostcall.Do(hostORMSearch, in, &out)
	return out.IDs, err
}

// Count runs q via the same host.orm.search call IDs makes, discarding
// the ID list — ignores Select, since counting doesn't fetch field data.
func (q *Query[T]) Count() (int64, error) {
	if q.cursor != "" {
		return 0, fmt.Errorf("orm: Query.Count does not support Cursor (use All for cursor pagination)")
	}
	var out abi.ORMSearchOutput
	in := abi.ORMSearchInput{Model: q.model, Domain: q.domain(), TxID: q.txID}
	err := hostcall.Do(hostORMSearch, in, &out)
	return out.Count, err
}
