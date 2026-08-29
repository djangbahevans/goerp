package tenantexport

import (
	"context"
	"fmt"

	"github.com/djangbahevans/goerp/internal/engine/jobqueue"
	"github.com/djangbahevans/goerp/internal/engine/tenant"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"
)

// Exporter satisfies adminapi.TenantExporter — the POST
// /admin/tenants/{slug}/export handler's entry point into this package.
type Exporter struct {
	tenantStore *tenant.Store
	jobClient   *river.Client[pgx.Tx]
	// jobQueue mirrors tenantoffboard.Offboarder's own field of the same
	// name — jobqueue.QueueAdmin in production, a private per-test queue
	// name in tests, for the same cross-test job-crosstalk reason that
	// package's doc comment explains.
	jobQueue string
}

func NewExporter(tenantStore *tenant.Store, jobClient *river.Client[pgx.Tx], jobQueue string) *Exporter {
	return &Exporter{tenantStore: tenantStore, jobClient: jobClient, jobQueue: jobQueue}
}

func (e *Exporter) StartExport(ctx context.Context, tenantSlug string, include, exclude []string) (jobID string, err error) {
	t, err := e.tenantStore.GetBySlug(ctx, tenantSlug)
	if err != nil {
		return "", fmt.Errorf("look up tenant %q: %w", tenantSlug, err)
	}

	insertResult, err := e.jobClient.Insert(ctx, Args{
		TenantID:   t.ID,
		TenantSlug: t.Slug,
		Include:    include,
		Exclude:    exclude,
	}, &river.InsertOpts{Queue: e.jobQueue})
	if err != nil {
		return "", fmt.Errorf("enqueue export job: %w", err)
	}
	return jobqueue.EncodeJobID(insertResult.Job.ID), nil
}
