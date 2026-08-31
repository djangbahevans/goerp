package schema

import (
	"context"
	"fmt"

	"github.com/djangbahevans/goerp/internal/engine/manifest"
	"github.com/djangbahevans/goerp/sdk/go/model"
)

// createUpdateEtagFunction is data-layer.md §2.4's own trigger function
// SQL, plus one addition beyond the doc's literal text: a guard on the
// app.skip_etag_trigger session variable (the same set_config-based
// per-transaction session-variable convention applyTenantScope already
// uses for app.current_user_id etc., internal/engine/wasm/tenant_scope.go).
// Without it, this BEFORE UPDATE trigger fires on every UPDATE regardless
// of which columns are in SET — including host_orm_write.go's
// applyComputedValue, whose own doc comment says it deliberately avoids
// rotating etag/updated_at for a computed-field recompute (single-hop
// only, no cascading, per the AC). A trigger has no way to see which
// columns a statement's SET clause named, only NEW's post-SET state, so
// the only way to keep that invariant once this trigger exists is for
// applyComputedValue to opt its own UPDATE out via this session variable.
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

// SyncEtagTriggers installs the update_etag() BEFORE UPDATE trigger
// (data-layer.md §2.4 "Etag trigger") on every table in the module's
// manifest audited_tables[] declaration, so every write path gets the
// same etag-hashing guarantee without reimplementing it. Runs after DDL
// apply (Execute) in the same schema-sync sequence as SyncRLSPolicies,
// over the same connection/session, so the audited tables already exist.
func (e *SchemaDiffEngine) SyncEtagTriggers(ctx context.Context, sess *SchemaSyncSession, modelDecls []model.ModelDeclaration, auditedTables []manifest.AuditedTable) error {
	if len(auditedTables) == 0 {
		return nil
	}

	if err := e.execWithRetry(ctx, sess.conn, createUpdateEtagFunction); err != nil {
		return fmt.Errorf("create update_etag function: %w", err)
	}

	for _, a := range auditedTables {
		table, err := resolveAuditedTableName(a, modelDecls)
		if err != nil {
			return err
		}

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
	return nil
}

// resolveAuditedTableName validates a's table name against modelDecls,
// mirroring resolvePolicyTarget's stance in rls.go: an audited_tables
// entry naming a table no declared model owns is a manifest error here,
// not a silent no-op — schema sync is this codebase's load-time
// validation layer (data-layer.md §2.4's "enforced by the schema diff
// engine" convention). Also requires the model to declare both etag and
// updated_at columns — update_etag() assigns NEW.etag/NEW.updated_at
// unconditionally, so a table missing either would only fail at its
// first UPDATE, with a cryptic Postgres "record NEW has no field"
// error, rather than here at sync time where the cause is clear.
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
