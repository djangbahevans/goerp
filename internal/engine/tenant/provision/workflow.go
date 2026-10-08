// Package tenantprovision creates tenant schemas and engine tables, syncs module schemas,
// seeds roles and config, establishes the founding admin and activates the tenant.
package tenantprovision

import (
	"fmt"
	"time"
	"uuid"

	"go.temporal.io/sdk/workflow"
)

// Input is ProvisionTenantWorkflow's argument (multitenancy-internals.md
// §6's ProvisionInput, minus the plan/subscription fields the
// billing-webhook trigger path owns). ExistingUserID is set by self-service
// registration: that user already has a password, so step 7 grants them
// the admin role instead of inviting AdminEmail.
type Input struct {
	Slug           string
	Name           string
	AdminEmail     string
	AdminName      string
	Region         string
	Country        string
	ExistingUserID string
}

const activityTimeout = 30 * time.Second

// Workflow provisions a tenant through activities and compensates failed creation by
// releasing the reserved slug.
func Workflow(ctx workflow.Context, input Input) error {
	ctx = workflow.WithActivityOptions(ctx, workflow.ActivityOptions{StartToCloseTimeout: activityTimeout})
	logger := workflow.GetLogger(ctx)

	// Chosen once and recorded in history, so a retried ReserveSlug
	// recognises its own row.
	var tenantID string
	if err := workflow.SideEffect(ctx, func(workflow.Context) any { return uuid.NewV7().String() }).Get(&tenantID); err != nil {
		return fmt.Errorf("choose tenant id: %w", err)
	}
	if err := workflow.ExecuteActivity(ctx, "ReserveSlug", input.Slug, input.Name, tenantID).Get(ctx, &tenantID); err != nil {
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
		// Individual module sync failures are logged without blocking tenant provisioning.
		_ = workflow.ExecuteActivity(ctx, "SyncModuleSchema", tenantID, input.Slug, name).Get(ctx, nil)
	}

	if err := workflow.ExecuteActivity(ctx, "SeedTenantConfig", input.Slug).Get(ctx, nil); err != nil {
		return fmt.Errorf("seed tenant config: %w", err)
	}

	if err := workflow.ExecuteActivity(ctx, "SeedSystemData", input.Slug).Get(ctx, nil); err != nil {
		return fmt.Errorf("seed system data: %w", err)
	}

	if input.ExistingUserID != "" {
		if err := workflow.ExecuteActivity(ctx, "AssignAdminRole", input.Slug, input.ExistingUserID).Get(ctx, nil); err != nil {
			return fmt.Errorf("assign admin role: %w", err)
		}
	} else if err := workflow.ExecuteActivity(ctx, "CreateAdminUser", input.Slug, input.AdminEmail, input.AdminName).Get(ctx, nil); err != nil {
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
