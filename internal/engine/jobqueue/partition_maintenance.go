package jobqueue

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/enginetables"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/riverqueue/river"
)

// PartitionMaintenanceArgs runs hourly to create future partitions for all pg_partman-
// registered tables.
type PartitionMaintenanceArgs struct{}

func (PartitionMaintenanceArgs) Kind() string { return "partition_maintenance" }

func (PartitionMaintenanceArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{Queue: QueueAdmin}
}

// PartitionMaintenanceWorker maintains every tenant's registered partitions using the
// schema-sync pool and revokes mutations on new append-only partitions. A nonzero
// EventLedgerRetention also configures event_deliveries retention and deletes expired rows
// from default partitions.
type PartitionMaintenanceWorker struct {
	river.WorkerDefaults[PartitionMaintenanceArgs]
	Pool                 *sql.DB
	EventLedgerRetention time.Duration
}

func (w *PartitionMaintenanceWorker) Work(ctx context.Context, job *river.Job[PartitionMaintenanceArgs]) error {
	if w.EventLedgerRetention > 0 {
		if err := w.setEventLedgerRetention(ctx); err != nil {
			return err
		}
	}
	if _, err := w.Pool.ExecContext(ctx, "SELECT partman.run_maintenance()"); err != nil {
		return fmt.Errorf("run partition maintenance: %w", err)
	}
	if err := enginetables.RevokeAppendOnlyMutations(ctx, w.Pool, ""); err != nil {
		return fmt.Errorf("revoke append-only partition privileges: %w", err)
	}
	if w.EventLedgerRetention > 0 {
		return w.purgeEventLedgerDefaultPartitions(ctx)
	}
	return nil
}

// ledgerParents lists every tenant's event_deliveries parent as the plain
// "schema.table" name pg_partman stores.
const ledgerParents = `parent_table LIKE 'tenant\_%.' || replace($1, '_', '\_')`

func (w *PartitionMaintenanceWorker) setEventLedgerRetention(ctx context.Context) error {
	retention := fmt.Sprintf("%d seconds", int64(w.EventLedgerRetention/time.Second))
	_, err := w.Pool.ExecContext(ctx, `
		UPDATE partman.part_config
		SET retention = $2, retention_keep_table = false, retention_keep_index = false
		WHERE `+ledgerParents+`
		  AND (retention IS DISTINCT FROM $2 OR retention_keep_table OR retention_keep_index)`,
		enginetables.EventDeliveriesTable, retention)
	if err != nil {
		return fmt.Errorf("set event ledger retention: %w", err)
	}
	return nil
}

// purgeEventLedgerDefaultPartitions deletes expired rows from each tenant's
// default partition, which holds the ledger rows of events too old for any
// dated partition (an administrator replay) and which pg_partman's retention
// never drops.
func (w *PartitionMaintenanceWorker) purgeEventLedgerDefaultPartitions(ctx context.Context) error {
	rows, err := w.Pool.QueryContext(ctx, `SELECT parent_table FROM partman.part_config WHERE `+ledgerParents, enginetables.EventDeliveriesTable)
	if err != nil {
		return fmt.Errorf("list event ledger tables: %w", err)
	}
	var parents []string
	for rows.Next() {
		var parent string
		if err := rows.Scan(&parent); err != nil {
			_ = rows.Close()
			return fmt.Errorf("scan event ledger table: %w", err)
		}
		parents = append(parents, parent)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("list event ledger tables: %w", err)
	}

	cutoff := time.Now().Add(-w.EventLedgerRetention)
	for _, parent := range parents {
		schema, table, _ := strings.Cut(parent, ".")
		defaultPartition := pgx.Identifier{schema, table + "_default"}.Sanitize()
		_, err := w.Pool.ExecContext(ctx, "DELETE FROM "+defaultPartition+" WHERE emitted_at < $1", cutoff)
		if isUndefinedRelation(err) {
			continue
		}
		if err != nil {
			return fmt.Errorf("purge %s: %w", defaultPartition, err)
		}
	}
	return nil
}

// isUndefinedRelation reports a table or schema dropped, e.g. by offboarding,
// after it was listed.
func isUndefinedRelation(err error) bool {
	pgErr, ok := errors.AsType[*pgconn.PgError](err)
	return ok && (pgErr.Code == "42P01" || pgErr.Code == "3F000")
}
