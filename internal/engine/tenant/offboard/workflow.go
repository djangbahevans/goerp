// Package tenantoffboard implements OffboardTenantWorkflow
// (multitenancy-internals.md §9) for the operator-triggered
// `POST /admin/tenants/{slug}/offboard` path: mark the tenant offboarding,
// wait out a grace period during which the offboard can still be
// cancelled, then delete its Meilisearch indexes, Redis cache entries,
// object storage files, and Postgres schema. Runs on systemworker.Worker
// (goerp#273) — the engine's own in-process Temporal worker — same
// convention as tenantprovision's ProvisionTenantWorkflow.
//
// The data-export step multitenancy-internals.md §9's own workflow sample
// includes (pg_dump + files archive + JSONL event export, zipped behind a
// signed URL, emailed to the tenant admin) is deliberately not implemented
// here: none of that infrastructure exists in this codebase yet, and it's
// the entire, separately-scoped subject of goerp#156 ("goerp tenant
// export"), itself still blocked on two unfiled prerequisites (backlog
// #19 field-access enforcement, backlog #944 resumable checkpoint
// mechanism). Building it inside this package would mean building #156 as
// a side effect of this one — the same "don't build into blocked
// territory" call #149 already made for ClickHouse/connector-table
// cleanup. This workflow goes straight from marking the tenant offboarding
// into the grace-period wait.
//
// Deletion is scoped to exactly what goerp#150's own AC lists — Postgres
// schema, Redis cache, object storage, Meilisearch — not the fuller
// cli-reference.md "Deterministic deletion scope" table's ClickHouse and
// connector_inbox/connector_webhook_endpoints rows, neither of which
// exists anywhere in this codebase yet either.
package tenantoffboard

import (
	"fmt"
	"time"

	"go.temporal.io/sdk/workflow"
)

// Input is OffboardTenantWorkflow's argument.
type Input struct {
	TenantID    string
	TenantSlug  string
	GracePeriod time.Duration
}

// activityTimeout bounds every activity below — generous relative to how
// fast each of these operations actually runs (a status update, an index
// delete, a cache flush, a schema drop), matching tenantprovision's own
// choice of the same value for the same reason: no per-step timeout is
// documented anywhere.
const activityTimeout = 30 * time.Second

// OffboardTenantWorkflow marks the tenant offboarding, waits out input.GracePeriod
// (during which CancelOffboard can still reverse it — see
// Activities.MarkDeletionStarted's doc comment for the race-safety
// mechanism), then deletes its search indexes, cache entries, storage
// files, and finally its Postgres schema, in that order. Object storage
// deletion must run before the schema drop, not after (multitenancy-
// internals.md §9's own illustrative step order has it the other way
// round) — DeleteTenantStorageFiles reads the tenant's `files` table to
// learn which object storage keys exist (object-storage-guide.md §12's
// purpose-first key layout means there's no tenant-scoped prefix to
// delete by any other way), and that table lives in the tenant's own
// schema, gone the moment DropTenantSchema runs.
func OffboardTenantWorkflow(ctx workflow.Context, input Input) error {
	ctx = workflow.WithActivityOptions(ctx, workflow.ActivityOptions{StartToCloseTimeout: activityTimeout})
	logger := workflow.GetLogger(ctx)

	if err := workflow.ExecuteActivity(ctx, "MarkOffboarding", input.TenantSlug).Get(ctx, nil); err != nil {
		return fmt.Errorf("mark tenant offboarding: %w", err)
	}

	if err := workflow.Sleep(ctx, input.GracePeriod); err != nil {
		return fmt.Errorf("grace period sleep: %w", err)
	}

	var deletionStarted bool
	if err := workflow.ExecuteActivity(ctx, "MarkDeletionStarted", input.TenantSlug).Get(ctx, &deletionStarted); err != nil {
		return fmt.Errorf("mark deletion started: %w", err)
	}
	if !deletionStarted {
		// CancelOffboard won the race during the grace period — the
		// tenant is already back to StatusActive. Nothing left to do.
		logger.Info("offboard cancelled during grace period, workflow exiting without deleting anything", "slug", input.TenantSlug)
		return nil
	}

	if err := workflow.ExecuteActivity(ctx, "DeleteSearchIndexes", input.TenantID).Get(ctx, nil); err != nil {
		return fmt.Errorf("delete search indexes: %w", err)
	}

	if err := workflow.ExecuteActivity(ctx, "FlushTenantCache", input.TenantID).Get(ctx, nil); err != nil {
		return fmt.Errorf("flush tenant cache: %w", err)
	}

	if err := workflow.ExecuteActivity(ctx, "DeleteTenantStorageFiles", input.TenantSlug).Get(ctx, nil); err != nil {
		return fmt.Errorf("delete tenant storage files: %w", err)
	}

	if err := workflow.ExecuteActivity(ctx, "DropTenantSchema", input.TenantSlug).Get(ctx, nil); err != nil {
		return fmt.Errorf("drop tenant schema: %w", err)
	}

	if err := workflow.ExecuteActivity(ctx, "MarkTenantDeleted", input.TenantSlug).Get(ctx, nil); err != nil {
		return fmt.Errorf("mark tenant deleted: %w", err)
	}

	logger.Info("tenant offboarded", "slug", input.TenantSlug, "tenantID", input.TenantID)
	return nil
}
