package db

import (
	"context"
	"database/sql"
	"fmt"
)

// Execer is the common surface *sql.DB and *sql.Tx both already satisfy —
// lets RegisterPartition run against either a plain pool or a caller's own
// held transaction (see that function's own doc comment for why the
// distinction matters).
type Execer interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// PartitionInterval and PartitionPremake are data-layer.md §2.6's
// documented values every partitioned table in this codebase uses so
// far: monthly range partitions, 3 months created ahead of need.
const (
	PartitionInterval = "1 month"
	PartitionPremake  = 3
)

// RegisterPartition registers parentTable (already created as
// PARTITION BY RANGE(controlColumn)) with pg_partman, which creates
// PartitionPremake months of partitions ahead of need and keeps them
// that way via the platform-wide partman.run_maintenance() periodic job
// (internal/engine/jobqueue.PartitionMaintenanceWorker).
//
// parentTable must be a plain, unquoted "schema.table" string —
// pg_partman's p_parent_table parameter is plain TEXT it parses itself
// (splitting on '.' and re-quoting internally), so a quoted identifier
// like tenantschema.Name's `"tenant_x"` form breaks its lookup with
// "Unable to find given parent table in system catalogs" even though the
// table demonstrably exists (goerp#194).
//
// Idempotent: partman.create_parent errors if the table is already a
// registered parent, so this checks partman.part_config first — needed
// since create_parent has no IF NOT EXISTS form of its own. That
// check-then-act is itself a race between two concurrent callers unless
// they're serialized: tenant provisioning's per-tenant call is naturally
// safe (Temporal runs at most one CreateEngineTables attempt per tenant
// at a time), but a package Bootstrap called at every engine startup
// (e.g. authaudit.Store.Bootstrap) can run from multiple replicas
// concurrently — pass a *sql.Tx already holding that package's own
// WithAdvisoryLock in that case, not the raw pool, so this runs
// serialized against every other Bootstrap caller the same way the
// table's own CREATE TABLE IF NOT EXISTS already is.
func RegisterPartition(ctx context.Context, exec Execer, parentTable, controlColumn string) error {
	var alreadyRegistered bool
	checkQuery := "SELECT EXISTS(SELECT 1 FROM partman.part_config WHERE parent_table = $1)"
	if err := exec.QueryRowContext(ctx, checkQuery, parentTable).Scan(&alreadyRegistered); err != nil {
		return fmt.Errorf("check partman registration for %s: %w", parentTable, err)
	}
	if alreadyRegistered {
		return nil
	}

	_, err := exec.ExecContext(ctx,
		"SELECT partman.create_parent(p_parent_table := $1, p_control := $2, p_interval := $3, p_premake := $4)",
		parentTable, controlColumn, PartitionInterval, PartitionPremake)
	if err != nil {
		return fmt.Errorf("register %s with pg_partman: %w", parentTable, err)
	}
	return nil
}
