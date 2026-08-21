package tenantoffboard

import (
	"context"
	"fmt"

	"github.com/djangbahevans/goerp/internal/engine/jobqueue"
	"github.com/djangbahevans/goerp/internal/engine/tenant"
	"github.com/riverqueue/river"
)

// OffboardImmediateArgs is the immediate-offboard path (cli-reference.md
// §5's `--immediate`): "runs deletion as an async job" per engine-
// internals.md §11a's own bucketing of it alongside module install/schema
// sync/tenant export, not OffboardTenantWorkflow with GracePeriod=0 —
// it's a different execution mechanism, not the same one with a shorter
// wait.
type OffboardImmediateArgs struct {
	TenantID   string
	TenantSlug string
}

func (OffboardImmediateArgs) Kind() string { return "tenant.offboard_immediate" }

func (OffboardImmediateArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{Queue: jobqueue.QueueAdmin}
}

// ImmediateWorker runs OffboardImmediateArgs by calling Activities'
// methods directly — no Temporal, no workflow wrapper, same
// implementation OffboardTenantWorkflow's activities call, just invoked
// as plain Go rather than through workflow.ExecuteActivity.
//
// Unlike a Temporal workflow (whose completed activities never re-run on
// retry, by construction — event-history replay), River re-invokes Work
// from scratch on every retry. Work is written to tolerate that: it reads
// the tenant's current status first and picks up from wherever a
// previous, interrupted attempt left off, rather than assuming it's
// starting fresh. It never calls MarkDeletionStarted — cancellation only
// applies to the grace-period path (cli-reference.md §5: "never valid
// after --immediate"), so there's no CAS guard to win here, only
// StatusActive to check for on the very first attempt.
type ImmediateWorker struct {
	river.WorkerDefaults[OffboardImmediateArgs]
	Activities  *Activities
	TenantStore *tenant.Store
}

func (w *ImmediateWorker) Work(ctx context.Context, job *river.Job[OffboardImmediateArgs]) error {
	a := job.Args

	t, err := w.TenantStore.GetBySlug(ctx, a.TenantSlug)
	if err != nil {
		return fmt.Errorf("look up tenant %q: %w", a.TenantSlug, err)
	}

	switch t.Status {
	case tenant.StatusDeleted:
		// A previous attempt already ran this to completion — nothing
		// left to do. Retrying past a terminal success is a no-op, not
		// an error.
		return nil
	case tenant.StatusActive:
		if err := w.Activities.MarkOffboarding(ctx, a.TenantSlug); err != nil {
			return err
		}
		fallthrough
	case tenant.StatusOffboarding:
		if err := w.Activities.DeleteSearchIndexes(ctx, a.TenantID); err != nil {
			return err
		}
		if err := w.Activities.FlushTenantCache(ctx, a.TenantID); err != nil {
			return err
		}
		if err := w.Activities.DeleteTenantStorageFiles(ctx, a.TenantSlug); err != nil {
			return err
		}
		if err := w.Activities.DropTenantSchema(ctx, a.TenantSlug); err != nil {
			return err
		}
		if err := w.Activities.MarkTenantDeleted(ctx, a.TenantSlug); err != nil {
			return err
		}
	default:
		return fmt.Errorf("tenant %q is in status %q, not eligible for immediate offboard", a.TenantSlug, t.Status)
	}

	return nil
}
