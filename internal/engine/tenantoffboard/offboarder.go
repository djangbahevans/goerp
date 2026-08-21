package tenantoffboard

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/adminapi"
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
	// jobQueue is the River queue name jobClient.Insert targets —
	// jobqueue.QueueAdmin in production, but a test constructs Offboarder
	// with its own private queue name so a per-test river.Client (built
	// directly, not via jobqueue.New) is the only client polling it. Every
	// river.Client built via jobqueue.New across every concurrently
	// running test package in this repo also registers QueueAdmin and
	// polls the same shared dev Postgres jobs table, so a job actually
	// inserted onto the literal "admin" queue can be picked up and
	// mishandled ("Unhandled job kind") by some other test's client
	// entirely — same cross-test-crosstalk risk the Temporal task queue
	// tests avoid with a per-test-unique taskQueue string.
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

// jobIDPrefix mirrors adminapi's own jobIDPrefix/encodeJobID (internal/
// engine/adminapi/jobs.go) — River's real int64 job ID prefixed with
// "job_", the wire format cli-reference.md's "IDs are strings" convention
// expects. Duplicated rather than exported from adminapi: it's exactly
// this one-line prefix-around-a-sequence-value, per that file's own doc
// comment, not worth a cross-package dependency to reuse.
const jobIDPrefix = "job_"

func encodeJobID(id int64) string {
	return jobIDPrefix + strconv.FormatInt(id, 10)
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
		return adminapi.OffboardResult{Status: "accepted", JobID: encodeJobID(insertResult.Job.ID)}, nil
	}

	// o.temporal is warn-only constructed in Engine.New (Temporal
	// unreachable at startup is not fail-hard for the engine as a whole)
	// and can legitimately be nil — every other caller of that field
	// already nil-guards it (workflowworker.spawn, systemworker.Worker,
	// tenantprovision.Provisioner).
	if o.temporal == nil {
		return adminapi.OffboardResult{}, fmt.Errorf("temporal client unavailable")
	}

	_, err = o.temporal.ExecuteWorkflow(ctx, client.StartWorkflowOptions{
		ID:        WorkflowID(tenantSlug),
		TaskQueue: o.taskQueue,
	}, Workflow, Input{
		TenantID:    t.ID,
		TenantSlug:  t.Slug,
		GracePeriod: gracePeriod,
	})
	if err != nil {
		return adminapi.OffboardResult{}, fmt.Errorf("start offboard workflow: %w", err)
	}

	deleteAt := time.Now().Add(gracePeriod)
	return adminapi.OffboardResult{Status: "scheduled", DeleteAt: &deleteAt}, nil
}

// CancelOffboard reverses a still-cancellable grace-period offboard.
// Purely a DB-level CAS (tenant.Store.CancelOffboarding) — no Temporal
// call needed: OffboardTenantWorkflow itself checks the same
// offboard_deletion_started_at guard the instant it wakes from its sleep
// (Activities.MarkDeletionStarted), so a workflow that's still sleeping
// when this succeeds simply wakes up later, finds it lost that race, logs
// it, and exits without deleting anything. Never valid for an immediate
// offboard (cli-reference.md §5): the immediate River job never calls
// MarkDeletionStarted at all, so by the time this could be called the
// tenant is already past StatusOffboarding — CancelOffboarding's own CAS
// (scoped to status = 'offboarding') already rejects that case.
func (o *Offboarder) CancelOffboard(ctx context.Context, tenantSlug string) error {
	if _, err := o.tenantStore.CancelOffboarding(ctx, tenantSlug); err != nil {
		return fmt.Errorf("cancel offboard for tenant %q: %w", tenantSlug, err)
	}
	return nil
}
