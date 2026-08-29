package schema

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/config"
	"github.com/djangbahevans/goerp/internal/engine/jobqueue"
	"github.com/djangbahevans/goerp/sdk/go/model"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
)

// widgetModelWithStatus mirrors widgetModel plus a "status" field, first
// declared as plain TEXT (no constraint) so a later re-declaration as
// model.Selection produces an AddCheck ModifyTable change instead of one
// baked into the initial CREATE TABLE — the only way to exercise the
// NOT-VALID-then-validate-later path this ticket adds.
func widgetModelWithStatusText() model.ModelDeclaration {
	return *model.Define("sales.widget", model.Table("widgets")).
		WithStandardFields().
		Field("name", model.Text().Required()).
		Field("sku", model.Char(40).Required()).
		Field("status", model.Text()).
		Index("idx_widgets_sku", model.BTreeIndex("sku").Unique())
}

func widgetModelWithStatusSelection(values ...string) model.ModelDeclaration {
	return *model.Define("sales.widget", model.Table("widgets")).
		WithStandardFields().
		Field("name", model.Text().Required()).
		Field("sku", model.Char(40).Required()).
		Field("status", model.Selection(values...)).
		Index("idx_widgets_sku", model.BTreeIndex("sku").Unique())
}

func constraintValid(t *testing.T, conn *sql.DB, schemaName, constraintName string) bool {
	t.Helper()
	var valid bool
	err := conn.QueryRow(`
		SELECT convalidated FROM pg_constraint c
		JOIN pg_namespace n ON n.oid = c.connamespace
		WHERE n.nspname = $1 AND c.conname = $2
	`, schemaName, constraintName).Scan(&valid)
	if err != nil {
		t.Fatalf("constraintValid query for %s.%s: %v", schemaName, constraintName, err)
	}
	return valid
}

func pendingValidationStatus(t *testing.T, conn *sql.DB, tenantID, tableName, constraintName string) (status string, errMsg sql.NullString, found bool) {
	t.Helper()
	err := conn.QueryRow(`
		SELECT status, error FROM system.pending_constraint_validations
		WHERE tenant_id = $1 AND table_name = $2 AND constraint_name = $3
	`, tenantID, tableName, constraintName).Scan(&status, &errMsg)
	switch {
	case err == sql.ErrNoRows:
		return "", sql.NullString{}, false
	case err != nil:
		t.Fatalf("pendingValidationStatus query: %v", err)
	}
	return status, errMsg, true
}

const widgetSyncTenantID = "44444444-4444-4444-4444-444444444444"

func TestExecute_AddCheckDeferredAsNotValid(t *testing.T) {
	sess, engine := setupTenantSchema(t, "difftest_deferredcheck")
	conn, _ := openTestPool(t, 5*time.Second)

	base := []model.ModelDeclaration{widgetModelWithStatusText()}
	changes, err := engine.Diff(context.Background(), sess, base, nil)
	if err != nil {
		t.Fatalf("Diff() error: %v", err)
	}
	if _, _, err := engine.ExecuteAccepted(context.Background(), sess, base, changes, nil); err != nil {
		t.Fatalf("Execute() error: %v", err)
	}

	// Insert a row whose status won't satisfy the constraint about to be
	// added — proves NOT VALID lets Execute succeed without validating
	// existing rows.
	if _, err := conn.Exec(
		`INSERT INTO tenant_difftest_deferredcheck.widgets (id, tenant_id, name, sku, status) VALUES (gen_random_uuid(), $1, 'w1', 'SKU1', 'archived')`,
		widgetSyncTenantID,
	); err != nil {
		t.Fatalf("seed violating row: %v", err)
	}

	withCheck := []model.ModelDeclaration{widgetModelWithStatusSelection("active", "inactive")}
	changes, err = engine.Diff(context.Background(), sess, withCheck, nil)
	if err != nil {
		t.Fatalf("second Diff() error: %v", err)
	}

	blocked, _, err := engine.ExecuteAccepted(context.Background(), sess, withCheck, changes, nil)
	if err != nil {
		t.Fatalf("Execute() with a deferred AddCheck against violating data errored (should have skipped validation via NOT VALID): %v", err)
	}
	if len(blocked) != 0 {
		t.Errorf("Execute() blocked = %v, want none — AddCheck is deferred, not blocked", blocked)
	}

	const constraintName = "widgets_status_check"
	if constraintValid(t, conn, "tenant_difftest_deferredcheck", constraintName) {
		t.Error("constraint is already convalidated — want NOT VALID immediately after Execute()")
	}

	status, _, found := pendingValidationStatus(t, conn, widgetSyncTenantID, "widgets", constraintName)
	if !found {
		t.Fatal("no system.pending_constraint_validations row was recorded")
	}
	if status != "pending" {
		t.Errorf("pending_constraint_validations status = %q, want %q", status, "pending")
	}
}

func TestValidateConstraintWorker_SucceedsWhenDataSatisfiesConstraint(t *testing.T) {
	sess, engine := setupTenantSchema(t, "difftest_validateok")
	conn, _ := openTestPool(t, 5*time.Second)

	base := []model.ModelDeclaration{widgetModelWithStatusText()}
	changes, err := engine.Diff(context.Background(), sess, base, nil)
	if err != nil {
		t.Fatalf("Diff() error: %v", err)
	}
	if _, _, err := engine.ExecuteAccepted(context.Background(), sess, base, changes, nil); err != nil {
		t.Fatalf("Execute() error: %v", err)
	}
	if _, err := conn.Exec(
		`INSERT INTO tenant_difftest_validateok.widgets (id, tenant_id, name, sku, status) VALUES (gen_random_uuid(), $1, 'w1', 'SKU1', 'active')`,
		widgetSyncTenantID,
	); err != nil {
		t.Fatalf("seed satisfying row: %v", err)
	}

	withCheck := []model.ModelDeclaration{widgetModelWithStatusSelection("active", "inactive")}
	changes, err = engine.Diff(context.Background(), sess, withCheck, nil)
	if err != nil {
		t.Fatalf("second Diff() error: %v", err)
	}
	if _, _, err := engine.ExecuteAccepted(context.Background(), sess, withCheck, changes, nil); err != nil {
		t.Fatalf("Execute() error: %v", err)
	}

	const constraintName = "widgets_status_check"
	worker := &ValidateConstraintWorker{Pool: conn}
	job := &river.Job[ValidateConstraintArgs]{Args: ValidateConstraintArgs{
		TenantID:       widgetSyncTenantID,
		TenantSlug:     "difftest_validateok",
		TableName:      "widgets",
		ConstraintName: constraintName,
	}}
	if err := worker.Work(context.Background(), job); err != nil {
		t.Fatalf("Work() error: %v", err)
	}

	if !constraintValid(t, conn, "tenant_difftest_validateok", constraintName) {
		t.Error("constraint is still NOT VALID after a successful Work()")
	}
	status, errMsg, found := pendingValidationStatus(t, conn, widgetSyncTenantID, "widgets", constraintName)
	if !found {
		t.Fatal("pending_constraint_validations row disappeared")
	}
	if status != "ok" {
		t.Errorf("status = %q, want %q", status, "ok")
	}
	if errMsg.Valid {
		t.Errorf("error = %q, want NULL on success", errMsg.String)
	}
}

func TestValidateConstraintWorker_RecordsFailureWithoutRetryingOnConstraintViolation(t *testing.T) {
	sess, engine := setupTenantSchema(t, "difftest_validatefail")
	conn, _ := openTestPool(t, 5*time.Second)

	base := []model.ModelDeclaration{widgetModelWithStatusText()}
	changes, err := engine.Diff(context.Background(), sess, base, nil)
	if err != nil {
		t.Fatalf("Diff() error: %v", err)
	}
	if _, _, err := engine.ExecuteAccepted(context.Background(), sess, base, changes, nil); err != nil {
		t.Fatalf("Execute() error: %v", err)
	}
	if _, err := conn.Exec(
		`INSERT INTO tenant_difftest_validatefail.widgets (id, tenant_id, name, sku, status) VALUES (gen_random_uuid(), $1, 'w1', 'SKU1', 'archived')`,
		widgetSyncTenantID,
	); err != nil {
		t.Fatalf("seed violating row: %v", err)
	}

	withCheck := []model.ModelDeclaration{widgetModelWithStatusSelection("active", "inactive")}
	changes, err = engine.Diff(context.Background(), sess, withCheck, nil)
	if err != nil {
		t.Fatalf("second Diff() error: %v", err)
	}
	if _, _, err := engine.ExecuteAccepted(context.Background(), sess, withCheck, changes, nil); err != nil {
		t.Fatalf("Execute() error: %v", err)
	}

	const constraintName = "widgets_status_check"
	worker := &ValidateConstraintWorker{Pool: conn}
	job := &river.Job[ValidateConstraintArgs]{Args: ValidateConstraintArgs{
		TenantID:       widgetSyncTenantID,
		TenantSlug:     "difftest_validatefail",
		TableName:      "widgets",
		ConstraintName: constraintName,
	}}
	if err := worker.Work(context.Background(), job); err != nil {
		t.Fatalf("Work() on a genuine constraint violation returned an error, want nil (terminal — data won't fix itself on retry): %v", err)
	}

	if constraintValid(t, conn, "tenant_difftest_validatefail", constraintName) {
		t.Error("constraint reports convalidated=true despite violating data")
	}
	status, errMsg, found := pendingValidationStatus(t, conn, widgetSyncTenantID, "widgets", constraintName)
	if !found {
		t.Fatal("pending_constraint_validations row disappeared")
	}
	if status != "failed" {
		t.Errorf("status = %q, want %q", status, "failed")
	}
	if !errMsg.Valid || errMsg.String == "" {
		t.Error("error was not recorded on a failed validation")
	}
}

func TestEnqueuePendingValidations_SecondSweepIsNoOp(t *testing.T) {
	sess, engine := setupTenantSchema(t, "difftest_sweep")
	conn, _ := openTestPool(t, 5*time.Second)

	base := []model.ModelDeclaration{widgetModelWithStatusText()}
	changes, err := engine.Diff(context.Background(), sess, base, nil)
	if err != nil {
		t.Fatalf("Diff() error: %v", err)
	}
	if _, _, err := engine.ExecuteAccepted(context.Background(), sess, base, changes, nil); err != nil {
		t.Fatalf("Execute() error: %v", err)
	}
	withCheck := []model.ModelDeclaration{widgetModelWithStatusSelection("active", "inactive")}
	changes, err = engine.Diff(context.Background(), sess, withCheck, nil)
	if err != nil {
		t.Fatalf("second Diff() error: %v", err)
	}
	if _, _, err := engine.ExecuteAccepted(context.Background(), sess, withCheck, changes, nil); err != nil {
		t.Fatalf("Execute() error: %v", err)
	}

	pgxPool, err := pgxpool.New(context.Background(), localSchemaSyncDSN)
	if err != nil {
		t.Fatalf("pgxpool.New: %v", err)
	}
	t.Cleanup(pgxPool.Close)

	if err := jobqueue.Migrate(context.Background(), pgxPool); err != nil {
		t.Fatalf("jobqueue.Migrate: %v", err)
	}
	workers := river.NewWorkers()
	river.AddWorker(workers, &ValidateConstraintWorker{Pool: conn})
	client, err := jobqueue.New(pgxPool, &config.Config{
		QueueCriticalConcurrency: 1, QueueDefaultConcurrency: 1, QueueBulkConcurrency: 1,
		QueueSearchConcurrency: 1, QueueEmailConcurrency: 1,
	}, workers)
	if err != nil {
		t.Fatalf("jobqueue.New: %v", err)
	}

	countJobs := func() int {
		var n int
		if err := conn.QueryRow(
			`SELECT count(*) FROM river_job WHERE kind = 'schema.validate_constraint' AND args->>'tenant_id' = $1`,
			widgetSyncTenantID,
		).Scan(&n); err != nil {
			t.Fatalf("count river_job rows: %v", err)
		}
		return n
	}

	if err := EnqueuePendingValidations(context.Background(), conn, client); err != nil {
		t.Fatalf("first EnqueuePendingValidations() error: %v", err)
	}
	firstCount := countJobs()
	if firstCount == 0 {
		t.Fatal("first sweep enqueued no jobs, want at least one for the deferred AddCheck")
	}

	if err := EnqueuePendingValidations(context.Background(), conn, client); err != nil {
		t.Fatalf("second EnqueuePendingValidations() error: %v", err)
	}
	if got := countJobs(); got != firstCount {
		t.Errorf("second sweep changed job count from %d to %d, want it to be a no-op", firstCount, got)
	}
}
