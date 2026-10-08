// Package tenantoffboard marks a tenant offboarding, waits through a cancellable grace
// period and deletes its Postgres schema, Redis entries, object files and search indexes.
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

const activityTimeout = 30 * time.Second

// Offboarding deletes storage objects before dropping the schema because the tenant files
// table is needed to enumerate purpose-prefixed object keys.
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
