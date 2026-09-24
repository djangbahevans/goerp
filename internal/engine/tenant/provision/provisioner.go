package tenantprovision

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/adminapi"
	"github.com/djangbahevans/goerp/internal/engine/temporal"
	"go.temporal.io/api/serviceerror"
	"go.temporal.io/sdk/client"
	sdktemporal "go.temporal.io/sdk/temporal"
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

// ErrSlugTaken reports that another provisioning run already holds the
// slug.
var ErrSlugTaken = errors.New("tenant slug is already taken")

// ErrProvisioningPending reports that the workflow started but ctx ended
// before it finished; it keeps running and will complete on its own.
var ErrProvisioningPending = errors.New("tenant provisioning is still running")

// ProvisionForRegistration runs ProvisionTenantWorkflow for self-service
// registration and waits for it to finish, granting userID the admin role.
// Unlike StartProvisioning it never attaches to an existing run for the
// slug: that run would be another registrant's tenant.
func (p *Provisioner) ProvisionForRegistration(ctx context.Context, slug, name, userID string) error {
	if p.temporal == nil {
		return fmt.Errorf("temporal client unavailable")
	}

	run, err := p.temporal.ExecuteWorkflow(ctx, client.StartWorkflowOptions{
		ID:                                       WorkflowID(slug),
		TaskQueue:                                p.taskQueue,
		WorkflowExecutionErrorWhenAlreadyStarted: true,
	}, Workflow, Input{Slug: slug, Name: name, ExistingUserID: userID})
	if err != nil {
		if _, ok := errors.AsType[*serviceerror.WorkflowExecutionAlreadyStarted](err); ok {
			return ErrSlugTaken
		}
		return fmt.Errorf("start provisioning workflow: %w", err)
	}
	if err := run.Get(ctx, nil); err != nil {
		if contextExpired(ctx) {
			return ErrProvisioningPending
		}
		// A reserved slug reads as taken to a registrant (auth-internals.md
		// §3 "Self-service registration").
		if hasApplicationErrorType(err, SlugTakenErrorType) || hasApplicationErrorType(err, SlugReservedErrorType) {
			return ErrSlugTaken
		}
		return fmt.Errorf("provision tenant %s: %w", slug, err)
	}
	return nil
}

// contextExpired reports whether ctx is done or past its deadline. The
// Temporal client's gRPC call can fail on the deadline before ctx's own
// timer marks it done, leaving ctx.Err() nil for that instant.
func contextExpired(ctx context.Context) bool {
	if ctx.Err() != nil {
		return true
	}
	deadline, ok := ctx.Deadline()
	return ok && !time.Now().Before(deadline)
}

// hasApplicationErrorType reports whether any ApplicationError in err's
// chain has errType. The workflow wraps an activity's failure in its own
// ApplicationError, so the first one found isn't the activity's.
func hasApplicationErrorType(err error, errType string) bool {
	for ; err != nil; err = errors.Unwrap(err) {
		if appErr, ok := err.(*sdktemporal.ApplicationError); ok && appErr.Type() == errType {
			return true
		}
	}
	return false
}
