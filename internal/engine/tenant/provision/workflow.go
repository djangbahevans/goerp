// Package tenantprovision implements ProvisionTenantWorkflow
// (multitenancy-internals.md §6) for the operator-triggered POST
// /admin/tenants path: reserve a slug, create the tenant's schema and
// engine-owned tables, sync every loaded module's schema against it,
// seed config and default roles, invite the founding admin, register its
// default subdomain, then activate it. Runs on systemworker.Worker
// (goerp#273) — the engine's own in-process Temporal worker, not
// internal/engine/workflowworker's per-module child-process mechanism,
// since this workflow belongs to the engine itself, not any module.
package tenantprovision

import (
	"fmt"
	"time"

	"go.temporal.io/sdk/workflow"
)

// Input is ProvisionTenantWorkflow's argument — the operator-triggered
// path's own fields (multitenancy-internals.md §6's ProvisionInput has
// further fields — ExistingUserID, plan/subscription — that belong to
// the self-service and billing-webhook trigger paths this ticket
// explicitly excludes; see goerp#149's own scope).
type Input struct {
	Slug       string
	Name       string
	AdminEmail string
	AdminName  string
	Region     string
	Country    string
}

// activityTimeout bounds every activity below — generous relative to
// multitenancy-internals.md §6's own "~4-6 seconds" total-duration
// estimate for the whole workflow, since no per-step timeout is
// documented anywhere.
const activityTimeout = 30 * time.Second

// Workflow orchestrates tenant creation as a sequence of activities
// (registered on Activities — see NewActivities), matching
// multitenancy-internals.md §6's ProvisionTenantWorkflow steps 1-4, 5-7,
// 8, 10 (steps 9 "CreateSubscription", 11 "EmitTenantCreatedEvent", and a
// separate step-12 welcome email are out of this ticket's scope — see
// goerp#149's own filed Scope/AC, and CreateAdminUser's doc comment for
// why no separate welcome-email step is needed).
func Workflow(ctx workflow.Context, input Input) error {
	ctx = workflow.WithActivityOptions(ctx, workflow.ActivityOptions{StartToCloseTimeout: activityTimeout})
	logger := workflow.GetLogger(ctx)

	var tenantID string
	if err := workflow.ExecuteActivity(ctx, "ReserveSlug", input.Slug, input.Name).Get(ctx, &tenantID); err != nil {
		return fmt.Errorf("reserve slug: %w", err)
	}

	if err := workflow.ExecuteActivity(ctx, "CreateTenantSchema", input.Slug).Get(ctx, nil); err != nil {
		// Compensate: release the slug reservation so a retried "tenant
		// create" call (or a different slug entirely) isn't permanently
		// blocked by this failed attempt. Awaited, unlike multitenancy-
		// internals.md §6's own fire-and-forget sample — an unawaited
		// compensation that itself fails would silently leave the slug
		// stuck, which is exactly the failure mode this step exists to
		// prevent.
		if compErr := workflow.ExecuteActivity(ctx, "ReleaseSlugReservation", tenantID).Get(ctx, nil); compErr != nil {
			logger.Error("release slug reservation after schema creation failure", "tenantID", tenantID, "error", compErr)
		}
		return fmt.Errorf("create tenant schema: %w", err)
	}

	if err := workflow.ExecuteActivity(ctx, "CreateEngineTables", input.Slug).Get(ctx, nil); err != nil {
		return fmt.Errorf("create engine tables: %w", err)
	}

	var moduleNames []string
	if err := workflow.ExecuteActivity(ctx, "ListModuleNames").Get(ctx, &moduleNames); err != nil {
		return fmt.Errorf("list modules: %w", err)
	}
	for _, name := range moduleNames {
		// SyncModuleSchema itself never returns an error (logs and
		// continues) — goerp#149's own AC: "individual module schema-sync
		// failures are logged but do not block overall tenant
		// provisioning."
		_ = workflow.ExecuteActivity(ctx, "SyncModuleSchema", tenantID, input.Slug, name).Get(ctx, nil)
	}

	if err := workflow.ExecuteActivity(ctx, "SeedTenantConfig", input.Slug).Get(ctx, nil); err != nil {
		return fmt.Errorf("seed tenant config: %w", err)
	}

	if err := workflow.ExecuteActivity(ctx, "SeedSystemData", input.Slug).Get(ctx, nil); err != nil {
		return fmt.Errorf("seed system data: %w", err)
	}

	if err := workflow.ExecuteActivity(ctx, "CreateAdminUser", input.Slug, input.AdminEmail).Get(ctx, nil); err != nil {
		return fmt.Errorf("create admin user: %w", err)
	}

	if err := workflow.ExecuteActivity(ctx, "RegisterDomain", tenantID, input.Slug).Get(ctx, nil); err != nil {
		return fmt.Errorf("register domain: %w", err)
	}

	if err := workflow.ExecuteActivity(ctx, "ActivateTenant", input.Slug).Get(ctx, nil); err != nil {
		return fmt.Errorf("activate tenant: %w", err)
	}

	logger.Info("tenant provisioned", "slug", input.Slug, "tenantID", tenantID)
	return nil
}
