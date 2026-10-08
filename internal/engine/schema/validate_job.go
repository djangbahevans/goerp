package schema

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/djangbahevans/goerp/internal/engine/jobqueue"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/riverqueue/river"
)

// ValidateConstraintArgs schedules background validation of a NOT VALID constraint.
type ValidateConstraintArgs struct {
	IdempotencyKey string `json:"idempotency_key" river:"unique"`
	TenantID       string `json:"tenant_id"`
	TenantSlug     string `json:"tenant_slug"`
	TableName      string `json:"table_name"`
	ConstraintName string `json:"constraint_name"`
}

func (ValidateConstraintArgs) Kind() string { return "schema.validate_constraint" }

func (ValidateConstraintArgs) InsertOpts() river.InsertOpts {
	opts := jobqueue.UniqueByIdempotencyKey()
	opts.Queue = jobqueue.QueueBulk
	return opts
}

func validationIdempotencyKey(tenantID, tableName, constraintName string) string {
	return fmt.Sprintf("%s:%s:%s", tenantID, tableName, constraintName)
}

// ValidateConstraintWorker runs ALTER TABLE ... VALIDATE CONSTRAINT for a
// NOT VALID constraint and records the outcome.
//
// Pool is the schema-sync pool, whose role owns the tables. Work runs SET
// LOCAL search_path and VALIDATE CONSTRAINT in one transaction.
type ValidateConstraintWorker struct {
	river.WorkerDefaults[ValidateConstraintArgs]
	Pool *sql.DB
}

func (w *ValidateConstraintWorker) Work(ctx context.Context, job *river.Job[ValidateConstraintArgs]) error {
	a := job.Args

	tx, err := w.Pool.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin validate-constraint transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx, "SET LOCAL search_path = "+quoteIdent("tenant_"+a.TenantSlug)); err != nil {
		return fmt.Errorf("set search_path for tenant %s: %w", a.TenantSlug, err)
	}

	stmt := fmt.Sprintf("ALTER TABLE %s VALIDATE CONSTRAINT %s", quoteIdent(a.TableName), quoteIdent(a.ConstraintName))
	if _, valErr := tx.ExecContext(ctx, stmt); valErr != nil {
		if isConstraintViolation(valErr) {
			// Terminal: the existing data violates the constraint, so
			// retrying without a data fix can't succeed. Surface the
			// failure on the tracking row instead of retrying forever.
			if err := recordValidationResult(ctx, w.Pool, a, "failed", valErr.Error()); err != nil {
				return err
			}
			return nil
		}
		return fmt.Errorf("validate constraint %s on %s: %w", a.ConstraintName, a.TableName, valErr)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit validate-constraint transaction: %w", err)
	}

	return recordValidationResult(ctx, w.Pool, a, "ok", "")
}

func recordValidationResult(ctx context.Context, pool *sql.DB, a ValidateConstraintArgs, status, errMsg string) error {
	var errVal any
	if errMsg != "" {
		errVal = errMsg
	}
	_, err := pool.ExecContext(ctx, `
		UPDATE system.pending_constraint_validations
		SET status = $1, error = $2, validated_at = NOW()
		WHERE tenant_id = $3 AND table_name = $4 AND constraint_name = $5
	`, status, errVal, a.TenantID, a.TableName, a.ConstraintName)
	if err != nil {
		return fmt.Errorf("record validation result for %s.%s: %w", a.TableName, a.ConstraintName, err)
	}
	return nil
}

func isConstraintViolation(err error) bool {
	pgErr, ok := errors.AsType[*pgconn.PgError](err)
	if !ok {
		return false
	}
	switch pgErr.Code {
	case "23514", "23503": // check_violation, foreign_key_violation
		return true
	default:
		return false
	}
}

// EnqueuePendingValidations sweeps persisted work after queue construction. River
// uniqueness makes repeated sweeps safe and recovers pending rows after startup crashes.
func EnqueuePendingValidations(ctx context.Context, pool *sql.DB, client *river.Client[pgx.Tx]) error {
	rows, err := pool.QueryContext(ctx, `
		SELECT tenant_id, tenant_slug, table_name, constraint_name
		FROM system.pending_constraint_validations
		WHERE status = 'pending'
	`)
	if err != nil {
		return fmt.Errorf("query pending constraint validations: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var pending []ValidateConstraintArgs
	for rows.Next() {
		var a ValidateConstraintArgs
		if err := rows.Scan(&a.TenantID, &a.TenantSlug, &a.TableName, &a.ConstraintName); err != nil {
			return fmt.Errorf("scan pending constraint validation: %w", err)
		}
		a.IdempotencyKey = validationIdempotencyKey(a.TenantID, a.TableName, a.ConstraintName)
		pending = append(pending, a)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate pending constraint validations: %w", err)
	}

	for _, a := range pending {
		if _, err := client.Insert(ctx, a, nil); err != nil {
			return fmt.Errorf("enqueue validation for %s.%s: %w", a.TableName, a.ConstraintName, err)
		}
	}

	return nil
}
