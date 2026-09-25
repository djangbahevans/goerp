package schema

import (
	"context"
	"fmt"
	"strings"

	"github.com/djangbahevans/goerp/internal/engine/tenantschema"
	"github.com/djangbahevans/goerp/sdk/go/model"
)

// grantTargetsQuery returns, schema-qualified and quoted, which of the
// tables in $1 exist, and the sequences they own (serial and identity
// columns).
const grantTargetsQuery = `
WITH t AS (
    SELECT to_regclass(name) AS oid FROM unnest($1::text[]) AS name
)
SELECT 'table', t.oid::regclass::text FROM t WHERE t.oid IS NOT NULL
UNION ALL
SELECT 'sequence', s.oid::regclass::text
FROM t
JOIN pg_depend d ON d.refobjid = t.oid
    AND d.classid = 'pg_class'::regclass AND d.refclassid = 'pg_class'::regclass
JOIN pg_class s ON s.oid = d.objid AND s.relkind = 'S'`

// SyncTenantRoleGrants gives the tenant role DML on each of modelDecls'
// tables and USAGE, SELECT on their sequences (data-layer.md §2.2). It
// does nothing for a tenant with no role, which module SQL can't run as.
func (e *SchemaDiffEngine) SyncTenantRoleGrants(ctx context.Context, sess *SchemaSyncSession, modelDecls []model.ModelDeclaration) error {
	var hasRole bool
	if err := sess.conn.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = $1)`, "tenant_"+sess.tenantSlug).Scan(&hasRole); err != nil {
		return fmt.Errorf("look up tenant role: %w", err)
	}
	if !hasRole {
		return nil
	}

	role := tenantschema.Name(sess.tenantSlug)
	var candidates []string
	for _, md := range modelDecls {
		if md.Backend == "" {
			candidates = append(candidates, role+"."+quoteIdent(TableNameFor(md)))
		}
	}
	if len(candidates) == 0 {
		return nil
	}

	rows, err := sess.conn.QueryContext(ctx, grantTargetsQuery, candidates)
	if err != nil {
		return fmt.Errorf("list tenant role grant targets: %w", err)
	}
	var tables, sequences []string
	for rows.Next() {
		var kind, name string
		if err := rows.Scan(&kind, &name); err != nil {
			_ = rows.Close()
			return fmt.Errorf("list tenant role grant targets: %w", err)
		}
		if kind == "table" {
			tables = append(tables, name)
		} else {
			sequences = append(sequences, name)
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("list tenant role grant targets: %w", err)
	}

	if len(tables) > 0 {
		if err := e.execWithRetry(ctx, sess.conn, "GRANT SELECT, INSERT, UPDATE, DELETE ON TABLE "+strings.Join(tables, ", ")+" TO "+role); err != nil {
			return fmt.Errorf("grant tenant role on module tables: %w", err)
		}
	}
	if len(sequences) > 0 {
		if err := e.execWithRetry(ctx, sess.conn, "GRANT USAGE, SELECT ON SEQUENCE "+strings.Join(sequences, ", ")+" TO "+role); err != nil {
			return fmt.Errorf("grant tenant role on module sequences: %w", err)
		}
	}
	return nil
}
