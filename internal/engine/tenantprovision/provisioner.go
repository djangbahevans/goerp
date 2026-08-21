package tenantprovision

import (
	"context"
	"fmt"

	"github.com/djangbahevans/goerp/internal/engine/adminapi"
	"github.com/djangbahevans/goerp/internal/engine/temporal"
	"go.temporal.io/sdk/client"
)

// Provisioner satisfies adminapi.Provisioner — the POST /admin/tenants
// handler's entry point into this package.
type Provisioner struct {
	temporal  *temporal.Client
	taskQueue string
}

func NewProvisioner(temporalClient *temporal.Client, taskQueue string) *Provisioner {
	return &Provisioner{temporal: temporalClient, taskQueue: taskQueue}
}

// WorkflowID derives ProvisionTenantWorkflow's Temporal workflow ID from
// slug, deterministically — the same ID a second POST /admin/tenants call
// for the same slug reuses. That's what makes cli-reference.md §5's
// documented "Idempotency-Key" retry behavior work without a separate
// response cache: client.StartWorkflowOptions.
// WorkflowExecutionErrorWhenAlreadyStarted defaults to false, so
// ExecuteWorkflow against an ID that's already running (or already
// completed, under the default WorkflowIDReusePolicy) returns a handle to
// that existing run instead of erroring — confirmed empirically, not just
// from the SDK doc comment, before relying on it here. Slug is already
// the natural idempotency key for tenant creation: two POSTs with the
// same slug inherently mean "the same intended tenant."
func WorkflowID(slug string) string {
	return "provision-tenant-" + slug
}

func (p *Provisioner) StartProvisioning(ctx context.Context, req adminapi.CreateTenantRequest) (string, error) {
	// p.temporal is warn-only constructed in Engine.New (Temporal
	// unreachable at startup is not fail-hard for the engine as a whole)
	// and can legitimately be nil — every other caller of that field
	// already nil-guards it (workflowworker.spawn, systemworker.Worker).
	if p.temporal == nil {
		return "", fmt.Errorf("temporal client unavailable")
	}

	workflowID := WorkflowID(req.Slug)

	_, err := p.temporal.ExecuteWorkflow(ctx, client.StartWorkflowOptions{
		ID:        workflowID,
		TaskQueue: p.taskQueue,
	}, Workflow, Input{
		Slug:       req.Slug,
		Name:       req.Name,
		AdminEmail: req.AdminEmail,
		AdminName:  req.AdminName,
		Region:     req.Region,
		Country:    req.Country,
	})
	if err != nil {
		return "", fmt.Errorf("start provisioning workflow: %w", err)
	}

	return workflowID, nil
}
