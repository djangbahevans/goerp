package schema

import (
	"context"
	"fmt"

	"github.com/djangbahevans/goerp/internal/engine/manifest"
	"github.com/djangbahevans/goerp/sdk/go/model"
)

// Computed recomputation sets app.skip_etag_trigger to preserve etag and updated_at; a
// BEFORE UPDATE trigger otherwise fires regardless of which columns changed.
const createUpdateEtagFunction = `
CREATE OR REPLACE FUNCTION update_etag()
RETURNS TRIGGER AS $$
BEGIN
    IF current_setting('app.skip_etag_trigger', true) IS DISTINCT FROM 'true' THEN
        NEW.etag = encode(sha256(
            (to_jsonb(NEW) - 'etag' - 'updated_at' - 'created_at' - 'id' - 'tenant_id')::text::bytea
        ), 'hex');
        NEW.updated_at = NOW();
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
`

// SyncEtagTriggers reconciles audited-table update triggers after DDL. Removing all
// triggers still needs the module's last model declarations; the shared update_etag
// function remains installed.
func (e *SchemaDiffEngine) SyncEtagTriggers(ctx context.Context, sess *SchemaSyncSession, modelDecls []model.ModelDeclaration, auditedTables []manifest.AuditedTable) error {
	if len(auditedTables) > 0 {
		if err := e.execWithRetry(ctx, sess.conn, createUpdateEtagFunction); err != nil {
			return fmt.Errorf("create update_etag function: %w", err)
		}
	}

	desired := make(map[string]bool, len(auditedTables))
	for _, a := range auditedTables {
		table, err := resolveAuditedTableName(a, modelDecls)
		if err != nil {
			return err
		}
		desired[table] = true

		triggerName := table + "_etag_trigger"
		dropStmt := fmt.Sprintf("DROP TRIGGER IF EXISTS %s ON %s", quoteIdent(triggerName), quoteIdent(table))
		if err := e.execWithRetry(ctx, sess.conn, dropStmt); err != nil {
			return fmt.Errorf("audited table %q: drop existing etag trigger: %w", table, err)
		}

		createStmt := fmt.Sprintf("CREATE TRIGGER %s BEFORE UPDATE ON %s FOR EACH ROW EXECUTE FUNCTION update_etag()",
			quoteIdent(triggerName), quoteIdent(table))
		if err := e.execWithRetry(ctx, sess.conn, createStmt); err != nil {
			return fmt.Errorf("audited table %q: create etag trigger: %w", table, err)
		}
	}

	return e.reconcileEtagTriggers(ctx, sess, modelDecls, desired)
}

// Reconcile triggers only on the module's declared tables. Preserve update_etag because
// other modules share that function.
func (e *SchemaDiffEngine) reconcileEtagTriggers(ctx context.Context, sess *SchemaSyncSession, modelDecls []model.ModelDeclaration, desired map[string]bool) error {
	schemaName := "tenant_" + sess.tenantSlug
	tables := dedupedOwnedTables(modelDecls)

	liveTables, err := listEtagTriggerTables(ctx, sess.conn, schemaName, tables)
	if err != nil {
		return fmt.Errorf("list etag triggers: %w", err)
	}

	for _, table := range liveTables {
		if desired[table] {
			continue
		}
		triggerName := table + "_etag_trigger"
		dropStmt := fmt.Sprintf("DROP TRIGGER IF EXISTS %s ON %s", quoteIdent(triggerName), quoteIdent(table))
		if err := e.execWithRetry(ctx, sess.conn, dropStmt); err != nil {
			return fmt.Errorf("drop etag trigger on %s: %w", table, err)
		}
	}

	return nil
}

// listEtagTriggerTables returns which of tables currently carry a live
// {table}_etag_trigger, in one round-trip via pg_trigger — the same
// catalog etag_trigger_test.go's own assertions already query — using
// pqStringArray/`= ANY($2)` (rls.go) to batch across every table.
func listEtagTriggerTables(ctx context.Context, execer execQuerier, schemaName string, tables []string) ([]string, error) {
	rows, err := execer.QueryContext(ctx, `
		SELECT c.relname
		FROM pg_trigger t
		JOIN pg_class c ON t.tgrelid = c.oid
		JOIN pg_namespace n ON c.relnamespace = n.oid
		WHERE n.nspname = $1 AND c.relname = ANY($2) AND t.tgname = c.relname || '_etag_trigger'
	`, schemaName, pqStringArray(tables))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var live []string
	for rows.Next() {
		var table string
		if err := rows.Scan(&table); err != nil {
			return nil, err
		}
		live = append(live, table)
	}
	return live, rows.Err()
}

// Audited targets must be declared models with etag and updated_at columns; otherwise the
// trigger would fail on the first update.
func resolveAuditedTableName(a manifest.AuditedTable, modelDecls []model.ModelDeclaration) (string, error) {
	for _, decl := range modelDecls {
		if TableNameFor(decl) != a.Table {
			continue
		}
		for _, col := range []string{"etag", "updated_at"} {
			if !hasField(decl, col) {
				return "", fmt.Errorf("audited_tables entry %q: model %q has no %q column (data-layer.md §2.4 standard table conventions)", a.Table, decl.Name, col)
			}
		}
		return a.Table, nil
	}
	return "", fmt.Errorf("audited_tables entry %q: no declared model owns this table", a.Table)
}

func hasField(decl model.ModelDeclaration, name string) bool {
	for _, f := range decl.Fields {
		if f.Name == name {
			return true
		}
	}
	return false
}
