// Package tenantprovision creates tenant schemas and engine tables, syncs module schemas,
// seeds roles and config, establishes the founding admin and activates the tenant.
package tenantprovision

import (
	"fmt"
	"time"
	"uuid"

	abiv1 "github.com/djangbahevans/goerp/contract/abi/v1"
	"go.temporal.io/sdk/workflow"
)

// Input identifies the tenant and founding admin. ExistingUserID grants an existing user
// the admin role instead of inviting AdminEmail; correlation IDs cross activity boundaries.
type Input struct {
	Slug           string
	Name           string
	AdminEmail     string
	AdminName      string
	Region         string
	Country        string
	RequestID      string
	TraceID        string
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
		// Await compensation so a failed reservation release cannot pass unnoticed.
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
	actor := abiv1.Request{ID: input.RequestID, TraceID: input.TraceID, UserID: input.ExistingUserID}
	for _, name := range moduleNames {
		// Individual module sync failures are logged without blocking tenant provisioning.
		_ = workflow.ExecuteActivity(ctx, "SyncModuleSchema", tenantID, input.Slug, name, actor).Get(ctx, nil)
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

	if err := workflow.ExecuteActivity(ctx, "NotifyTenantProvisioned", tenantID, input.Slug, actor).Get(ctx, nil); err != nil {
		logger.Error("tenant provisioned notification failed", "error", err)
	}

	logger.Info("tenant provisioned", "slug", input.Slug, "tenantID", tenantID)
	return nil
}
