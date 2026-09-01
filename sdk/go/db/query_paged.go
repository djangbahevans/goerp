package db

import (
	"fmt"
	"strconv"
)

// QueryPaged runs sql — a SELECT with no LIMIT/OFFSET of its own — as one
// page of up to limit rows, appending its own positional LIMIT/OFFSET
// parameters after params. There's no host.db ABI support for cursor
// pagination (host.orm.search's own cursor is computed engine-side,
// against a known model's columns); QueryPaged operates on arbitrary,
// caller-written SQL with no such column knowledge, so cursor here is an
// opaque offset token rather than a keyset cursor — "" starts at the
// first page, and the returned nextCursor threads straight back into the
// next call's own cursor argument.
func QueryPaged[T any](sql string, cursor string, limit int, params []any, opts ...QueryOption) ([]T, string, error) {
	if limit <= 0 {
		return nil, "", fmt.Errorf("db: QueryPaged limit must be positive, got %d", limit)
	}
	offset, err := decodeCursor(cursor)
	if err != nil {
		return nil, "", err
	}

	n := len(params)
	paged := fmt.Sprintf("%s LIMIT $%d OFFSET $%d", sql, n+1, n+2)
	pagedParams := append(append([]any{}, params...), limit+1, offset)

	rows, err := Query[T](paged, pagedParams, opts...)
	if err != nil {
		return nil, "", err
	}
	page, nextCursor := pagedRows(rows, limit, offset)
	return page, nextCursor, nil
}

// decodeCursor parses cursor back into the offset it encodes. "" (the
// first page) decodes to 0.
func decodeCursor(cursor string) (int, error) {
	if cursor == "" {
		return 0, nil
	}
	offset, err := strconv.Atoi(cursor)
	if err != nil || offset < 0 {
		return 0, fmt.Errorf("db: invalid cursor %q", cursor)
	}
	return offset, nil
}

// pagedRows trims rows — fetched with a limit+1 cap — back down to at
// most limit, and derives nextCursor from whether that extra row came
// back: exactly limit+1 rows means there's a further page starting at
// offset+limit, anything fewer means rows already holds the last page.
func pagedRows[T any](rows []T, limit, offset int) ([]T, string) {
	if len(rows) > limit {
		return rows[:limit], strconv.Itoa(offset + limit)
	}
	return rows, ""
}
