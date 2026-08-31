package wasm

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/manifest"
	"github.com/djangbahevans/goerp/internal/engine/schema"
	"github.com/djangbahevans/goerp/sdk/go/model"
)

// computedEtagWidgetModelDecl declares a table with the standard
// etag/updated_at columns data-layer.md §2.4 requires, plus one plain
// domain field (score) applyComputedValue writes directly — the shape
// that exposed goerp#455's own applyComputedValue/update_etag() trigger
// interaction: schema.SyncEtagTriggers installs a real BEFORE UPDATE
// trigger on this table when it's declared audited, and
// applyComputedValue must still leave etag/updated_at untouched despite
// that trigger being present.
func computedEtagWidgetModelDecl() model.ModelDeclaration {
	return model.ModelDeclaration{
		Name:  "widget",
		Table: "computed_etag_widgets",
		Fields: []model.NamedField{
			{Name: "id", Def: model.UUID().Required().PrimaryKey()},
			{Name: "tenant_id", Def: model.UUID().Required()},
			{Name: "etag", Def: model.Text()},
			{Name: "updated_at", Def: model.TimestampTZ()},
			{Name: "score", Def: model.Integer()},
		},
	}
}

// createAndTriggerFixtureComputedEtagWidgetTable creates the table
// directly (self-contained DDL, matching this file's neighbors) and
// installs the real update_etag() trigger via schema.SyncEtagTriggers —
// not a re-typed copy of the trigger SQL — so this test exercises the
// actual mechanism applyComputedValue coordinates with, not a stand-in.
func createAndTriggerFixtureComputedEtagWidgetTable(t *testing.T, conn *sql.DB, slug string) {
	t.Helper()
	ctx := context.Background()
	schemaName := "tenant_" + slug

	if _, err := conn.ExecContext(ctx, `CREATE TABLE `+schemaName+`.computed_etag_widgets (
		id UUID PRIMARY KEY,
		tenant_id UUID NOT NULL,
		etag TEXT NOT NULL DEFAULT '',
		updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
		score INTEGER
	)`); err != nil {
		t.Fatalf("create computed_etag_widgets table: %v", err)
	}

	pool := schema.NewPool(conn, 5*time.Second)
	if err := pool.Bootstrap(ctx); err != nil {
		t.Fatalf("schema.Bootstrap: %v", err)
	}
	sess, err := pool.BeginSync(ctx, "44444444-4444-4444-4444-444444444444", slug, "testmodule", &manifest.Manifest{Version: "1.0.0"})
	if err != nil {
		t.Fatalf("BeginSync: %v", err)
	}
	t.Cleanup(func() { _ = sess.Close(context.Background()) })

	engine := schema.NewSchemaDiffEngine(&schema.Config{})
	modelDecls := []model.ModelDeclaration{computedEtagWidgetModelDecl()}
	auditedTables := []manifest.AuditedTable{{Table: "computed_etag_widgets"}}
	if err := engine.SyncEtagTriggers(ctx, sess, modelDecls, auditedTables); err != nil {
		t.Fatalf("SyncEtagTriggers: %v", err)
	}
}

func readComputedEtagWidget(t *testing.T, tx *sql.Tx, id string) (etag string, updatedAt time.Time, score int) {
	t.Helper()
	if err := tx.QueryRow("SELECT etag, updated_at, score FROM computed_etag_widgets WHERE id = $1", id).Scan(&etag, &updatedAt, &score); err != nil {
		t.Fatalf("read row: %v", err)
	}
	return etag, updatedAt, score
}

func TestApplyComputedValue_TriggerInstalled_DoesNotRotateEtag(t *testing.T) {
	primaryDB := openTestPrimaryDB(t)
	ctx := context.Background()

	slug := fmt.Sprintf("computedetag%d", time.Now().UnixNano())
	createFixtureTenantSchema(t, primaryDB, slug)
	createAndTriggerFixtureComputedEtagWidgetTable(t, primaryDB, slug)

	tx, err := primaryDB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	t.Cleanup(func() { _ = tx.Rollback() })
	if _, err := tx.ExecContext(ctx, "SET search_path = "+quoteIdentORM("tenant_"+slug)); err != nil {
		t.Fatalf("set search_path: %v", err)
	}

	id := "10000000-0000-0000-0000-000000000001"
	if _, err := tx.ExecContext(ctx, "INSERT INTO computed_etag_widgets (id, tenant_id, score) VALUES ($1, gen_random_uuid(), 1)", id); err != nil {
		t.Fatalf("insert row: %v", err)
	}
	etagBefore, updatedAtBefore, _ := readComputedEtagWidget(t, tx, id)

	md := computedEtagWidgetModelDecl()
	if hostErr := applyComputedValue(ctx, tx, md, "id", id, "score", 99); hostErr != nil {
		t.Fatalf("applyComputedValue: %+v", hostErr)
	}

	etagAfter, updatedAtAfter, scoreAfter := readComputedEtagWidget(t, tx, id)
	if scoreAfter != 99 {
		t.Fatalf("score after applyComputedValue = %d, want 99", scoreAfter)
	}
	if etagAfter != etagBefore {
		t.Errorf("etag changed by applyComputedValue: before %q, after %q — the trigger fired despite app.skip_etag_trigger", etagBefore, etagAfter)
	}
	if !updatedAtAfter.Equal(updatedAtBefore) {
		t.Errorf("updated_at changed by applyComputedValue: before %v, after %v", updatedAtBefore, updatedAtAfter)
	}

	// A real, non-computed-field UPDATE on the same row, in the same
	// transaction, must still rotate etag normally — proving
	// app.skip_etag_trigger's SET LOCAL scope from applyComputedValue's
	// own bracketing doesn't leak into unrelated writes. Not asserting on
	// updated_at here: NOW() is transaction-scoped in Postgres (same
	// value for every statement in one transaction), so it can't be
	// expected to advance without committing between statements.
	if _, err := tx.ExecContext(ctx, "UPDATE computed_etag_widgets SET score = $1 WHERE id = $2", 100, id); err != nil {
		t.Fatalf("real update: %v", err)
	}
	etagFinal, _, _ := readComputedEtagWidget(t, tx, id)
	if etagFinal == etagAfter {
		t.Error("etag did not change on a real UPDATE — app.skip_etag_trigger leaked past applyComputedValue's own transaction scope")
	}
}
