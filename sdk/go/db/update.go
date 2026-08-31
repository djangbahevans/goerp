package db

import (
	"fmt"
	"strings"
)

// UpdateByID reads table's row's current etag, then issues an
// etag-checked UPDATE. A stale write surfaces as ErrEtagMismatch.
func UpdateByID(table, id string, patch map[string]any) (ExecResult, error) {
	if err := validateIdentifier(table); err != nil {
		return ExecResult{}, err
	}
	if len(patch) == 0 {
		return ExecResult{}, fmt.Errorf("db: UpdateByID patch is empty")
	}

	// Every input is validated before any I/O — including the etag read
	// below, which would otherwise run needlessly against a patch that's
	// going to fail validation anyway.
	cols := make([]string, 0, len(patch))
	vals := make([]any, 0, len(patch))
	for col, val := range patch {
		if err := validateIdentifier(col); err != nil {
			return ExecResult{}, err
		}
		cols = append(cols, col)
		vals = append(vals, val)
	}

	etag, err := currentEtag(table, id)
	if err != nil {
		return ExecResult{}, err
	}

	setClauses := make([]string, len(cols))
	for i, col := range cols {
		setClauses[i] = fmt.Sprintf("%s = $%d", col, i+1)
	}
	idPlaceholder := len(cols) + 1
	etagPlaceholder := len(cols) + 2
	sql := fmt.Sprintf("UPDATE %s SET %s WHERE id = $%d AND etag = $%d", table, strings.Join(setClauses, ", "), idPlaceholder, etagPlaceholder)

	params := append(vals, id, etag)
	return exec(dbExecInput{SQL: sql, Params: params, Opts: dbExecOpts{ExpectRows: true}})
}

// etagRow is currentEtag's own single-column mapping target.
type etagRow struct {
	Etag string `db:"etag"`
}

// currentEtag reads table's row's own etag column for the row identified
// by id, returning ErrNotFound if no such row exists.
func currentEtag(table, id string) (string, error) {
	rows, err := Query[etagRow](fmt.Sprintf("SELECT etag FROM %s WHERE id = $1", table), []any{id})
	if err != nil {
		return "", wrapExecError(err)
	}
	if len(rows) == 0 {
		return "", ErrNotFound
	}
	return rows[0].Etag, nil
}
