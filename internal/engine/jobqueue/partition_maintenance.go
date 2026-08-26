package jobqueue

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/riverqueue/river"
)

// PartitionMaintenanceArgs is the platform-wide (not per-tenant) periodic
// job that keeps every pg_partman-registered table's future partitions
// created ahead of need (goerp#194, data-layer.md §2.6 "Partition
// management") — registered as an hourly river.PeriodicJob in New, not
// inserted by any caller.
type PartitionMaintenanceArgs struct{}

func (PartitionMaintenanceArgs) Kind() string { return "partition_maintenance" }

func (PartitionMaintenanceArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{Queue: QueueAdmin}
}

// PartitionMaintenanceWorker calls partman.run_maintenance() once, which
// iterates every table any tenant schema has registered with pg_partman
// (event_log/audit_log today) and creates any partitions that have fallen
// short of that table's own p_premake window — a single call covers every
// tenant schema, not one call per tenant.
type PartitionMaintenanceWorker struct {
	river.WorkerDefaults[PartitionMaintenanceArgs]
	Pool *sql.DB
}

func (w *PartitionMaintenanceWorker) Work(ctx context.Context, job *river.Job[PartitionMaintenanceArgs]) error {
	if _, err := w.Pool.ExecContext(ctx, "SELECT partman.run_maintenance()"); err != nil {
		return fmt.Errorf("run partition maintenance: %w", err)
	}
	return nil
}
