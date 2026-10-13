// Package transfer defines tenant archive metadata and identifies portable table columns.
package transfer

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json/jsontext"
	"fmt"
	"slices"
	"strings"

	"github.com/djangbahevans/goerp/internal/engine/enginetables"
	"github.com/jackc/pgx/v5"
)

type Table struct {
	Name string `json:"name"`
	File string `json:"file"`
}

type Account struct {
	ID    string `json:"id"`
	Email string `json:"email"`
	Name  string `json:"name"`
}

type Record struct {
	Model  string                    `json:"model"`
	Record map[string]jsontext.Value `json:"record"`
}

type Column struct {
	Name string
	User bool
}

func Tables() []Table {
	var tables []Table
	for _, group := range enginetables.Groups {
		for _, table := range group.Tables {
			if table.Name != "tenant_invitations" {
				tables = append(tables, Table{Name: table.Name, File: "engine/" + table.Name + ".jsonl"})
			}
		}
	}
	return tables
}

// User identities have no cross-schema foreign keys in engine tables. Explicit identity
// names cover these columns; catalog foreign keys cover module-defined account references.
func Columns(ctx context.Context, tx *sql.Tx, schema, table string) ([]Column, error) {
	rows, err := tx.QueryContext(ctx, `
 SELECT a.attname, a.atttypid = 'uuid'::regtype AND (
  a.attname = ANY($3) OR EXISTS (
   SELECT 1 FROM pg_constraint fk
   WHERE fk.conrelid = c.oid AND fk.contype = 'f' AND a.attnum = ANY(fk.conkey)
    AND fk.confrelid = to_regclass('system.users')
  )
 )
 FROM pg_attribute a JOIN pg_class c ON c.oid = a.attrelid
 JOIN pg_namespace n ON n.oid = c.relnamespace
 WHERE n.nspname = $1 AND c.relname = $2 AND a.attnum > 0 AND NOT a.attisdropped
 ORDER BY a.attnum`, schema, table, []string{
		"user_id", "created_by", "updated_by", "granted_by", "suspended_by", "uploaded_by",
		"changed_by", "shared_by", "shared_with_user_id", "author_id", "assignee_id", "done_by",
	})
	if err != nil {
		return nil, fmt.Errorf("read columns for %s: %w", table, err)
	}
	defer rows.Close()

	var cols []Column
	for rows.Next() {
		var col Column
		if err := rows.Scan(&col.Name, &col.User); err != nil {
			return nil, err
		}
		cols = append(cols, col)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(cols) == 0 {
		return nil, fmt.Errorf("table %q has no columns", table)
	}
	return cols, nil
}

func ColumnNames(cols []Column) []string {
	names := make([]string, len(cols))
	for i, col := range cols {
		names[i] = col.Name
	}
	return names
}

func Quote(name string) string { return pgx.Identifier{name}.Sanitize() }

func Filter(table string) string {
	switch table {
	case "module_config":
		return " WHERE NOT encrypted"
	// Push registration tokens are credentials; omitting the complete row preserves the
	// table's NOT NULL constraint without putting a usable token in the archive.
	case "user_device_tokens":
		return " WHERE false"
	default:
		return ""
	}
}

func KnownTable(name string) bool {
	return slices.ContainsFunc(Tables(), func(t Table) bool { return t.Name == name })
}

// PostgreSQL constructs JSON before driver decoding so UUID, JSONB, bytea and large
// integers retain their database representation through the archive.
func Dump(ctx context.Context, tx *sql.Tx, table, model string, columns []string, filter, order string) ([]byte, error) {
	quoted := make([]string, len(columns))
	for i, col := range columns {
		quoted[i] = Quote(col)
	}
	query := "SELECT jsonb_build_object('model', $1::text, 'record', to_jsonb(r)) FROM (SELECT " + strings.Join(quoted, ",") + " FROM " + Quote(table) + filter + order + ") r"
	rows, err := tx.QueryContext(ctx, query, model)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var data bytes.Buffer
	for rows.Next() {
		var line []byte
		if err := rows.Scan(&line); err != nil {
			return nil, err
		}
		data.Write(line)
		data.WriteByte('\n')
	}
	return data.Bytes(), rows.Err()
}
