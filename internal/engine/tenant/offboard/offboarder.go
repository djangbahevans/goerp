package tenantoffboard

import (
	"context"
	"fmt"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/adminapi"
	"github.com/djangbahevans/goerp/internal/engine/jobqueue"
	"github.com/djangbahevans/goerp/internal/engine/temporal"
	"github.com/djangbahevans/goerp/internal/engine/tenant"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"
	"go.temporal.io/sdk/client"
)

// Offboarder satisfies adminapi.Offboarder — the POST /admin/tenants/
// {slug}/offboard and .../offboard/cancel handlers' entry point into this
// package.
type Offboarder struct {
	tenantStore *tenant.Store
	temporal    *temporal.Client
	taskQueue   string
	jobClient   *river.Client[pgx.Tx]
	// Tests use private queue names so other test processes cannot claim offboard jobs
	// from the shared database.
	jobQueue string
}

func NewOffboarder(tenantStore *tenant.Store, temporalClient *temporal.Client, taskQueue string, jobClient *river.Client[pgx.Tx], jobQueue string) *Offboarder {
	return &Offboarder{tenantStore: tenantStore, temporal: temporalClient, taskQueue: taskQueue, jobClient: jobClient, jobQueue: jobQueue}
}

// WorkflowID derives OffboardTenantWorkflow's Temporal workflow ID from
// slug, deterministically — same convention as tenantprovision.WorkflowID,
// though unlike provisioning this ID is never relied on for idempotent
// retry (an offboard call isn't naturally re-postable the way tenant
// create is): it just gives CancelOffboard's DB-only cancellation
// mechanism (Activities.MarkDeletionStarted's doc comment) a predictable
// name to log against.
func WorkflowID(slug string) string {
	return "offboard-tenant-" + slug
}

// StartOffboard starts either OffboardTenantWorkflow (the default,
// grace-period path) or an OffboardImmediateArgs River job (immediate:
// true), matching the two shapes adminapi.OffboardResult documents.
func (o *Offboarder) StartOffboard(ctx context.Context, tenantSlug string, gracePeriod time.Duration, immediate bool) (adminapi.OffboardResult, error) {
	t, err := o.tenantStore.GetBySlug(ctx, tenantSlug)
	if err != nil {
		return adminapi.OffboardResult{}, fmt.Errorf("look up tenant %q: %w", tenantSlug, err)
	}

	if immediate {
		insertResult, err := o.jobClient.Insert(ctx, OffboardImmediateArgs{
			TenantID:   t.ID,
			TenantSlug: t.Slug,
		}, &river.InsertOpts{Queue: o.jobQueue})
		if err != nil {
			return adminapi.OffboardResult{}, fmt.Errorf("enqueue immediate offboard job: %w", err)
		}
		return adminapi.OffboardResult{Status: "accepted", JobID: jobqueue.EncodeJobID(insertResult.Job.ID)}, nil
	}

	if o.temporal == nil {
		return adminapi.OffboardResult{}, fmt.Errorf("temporal client unavailable")
	}

	_, err = o.temporal.ExecuteWorkflow(ctx, client.StartWorkflowOptions{
		ID:        WorkflowID(tenantSlug),
		TaskQueue: o.taskQueue,
	}, OffboardTenantWorkflow, Input{
		TenantID:    t.ID,
		TenantSlug:  t.Slug,
		GracePeriod: gracePeriod,
	})
	if err != nil {
		return adminapi.OffboardResult{}, fmt.Errorf("start offboard workflow: %w", err)
	}

	return adminapi.OffboardResult{Status: "scheduled", DeleteAt: new(time.Now().Add(gracePeriod))}, nil
}

// CancelOffboard uses the deletion-start compare-and-swap to reverse a cancellable grace
// period. The workflow observes cancellation when it wakes; immediate offboards cannot be
// cancelled.
func (o *Offboarder) CancelOffboard(ctx context.Context, tenantSlug string) error {
	if _, err := o.tenantStore.CancelOffboarding(ctx, tenantSlug); err != nil {
		return fmt.Errorf("cancel offboard for tenant %q: %w", tenantSlug, err)
	}
	return nil
}
