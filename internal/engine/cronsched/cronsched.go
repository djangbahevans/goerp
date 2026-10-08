// Package cronsched fires module cron jobs (manifest-spec.md §16): once a
// minute it enqueues, for each cron job scheduled for that minute, one
// handle_cron job per active tenant that has the module enabled.
package cronsched

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/jobqueue"
	"github.com/djangbahevans/goerp/internal/engine/module"
	"github.com/djangbahevans/goerp/internal/engine/registry"
	"github.com/djangbahevans/goerp/internal/engine/tenant"
	tenantresolve "github.com/djangbahevans/goerp/internal/engine/tenant/resolve"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
	"github.com/rs/zerolog/log"
	"uuid"
)

// maxAttempts is a cron run's attempt limit, the default of a job type
// (manifest-spec.md §15): a run that keeps failing is not owed 25 retries.
const maxAttempts = 3

// TenantLister lists the tenants a cron job may run for; satisfied by *tenant.Store.
type TenantLister interface {
	ActiveTenants(ctx context.Context) ([]tenant.Tenant, error)
}

// EntitlementLoader resolves a tenant's enabled modules; satisfied by *tenantresolve.Resolver.
type EntitlementLoader interface {
	LoadEntitlements(ctx context.Context, tenantID string) (tenantresolve.EntitlementSet, error)
}

// Inserter is the part of the River client the worker enqueues with.
type Inserter interface {
	InsertMany(ctx context.Context, params []river.InsertManyParams) ([]*rivertype.JobInsertResult, error)
}

// Worker works jobqueue.CronTickArgs.
type Worker struct {
	river.WorkerDefaults[jobqueue.CronTickArgs]
	Registry     *registry.ModuleRegistry
	Tenants      TenantLister
	Entitlements EntitlementLoader
	// Inserter defaults to the River client working the job.
	Inserter Inserter
}

// Timeout overrides River's one-minute default, which a fan-out over many
// tenants can exceed.
func (w *Worker) Timeout(*river.Job[jobqueue.CronTickArgs]) time.Duration {
	return 5 * time.Minute
}

// Work enqueues every cron job due at the tick's minute. A tenant that fails
// does not stop the rest; the error is returned afterwards so River retries
// the tick, and the idempotency key keeps the retry from enqueueing a run
// twice.
func (w *Worker) Work(ctx context.Context, job *river.Job[jobqueue.CronTickArgs]) error {
	at := job.Args.At.UTC().Truncate(time.Minute)

	snap := w.Registry.Snapshot()
	if snap == nil {
		return errors.New("module registry has no snapshot yet")
	}
	due := dueEntries(snap, at)
	if len(due) == 0 {
		return nil
	}

	tenants, err := w.Tenants.ActiveTenants(ctx)
	if err != nil {
		return fmt.Errorf("list active tenants: %w", err)
	}

	var failures []error
	for _, tn := range tenants {
		ents, err := w.Entitlements.LoadEntitlements(ctx, tn.ID)
		if err != nil {
			failures = append(failures, fmt.Errorf("tenant %s: load entitlements: %w", tn.Slug, err))
			continue
		}
		var params []river.InsertManyParams
		for _, entry := range due {
			if ents.ModuleEnabled(entry.Module) {
				params = append(params, cronJobParams(entry, tn.ID, at))
			}
		}
		if len(params) == 0 {
			continue
		}
		if err := w.insert(ctx, params); err != nil {
			failures = append(failures, fmt.Errorf("tenant %s: enqueue cron jobs: %w", tn.Slug, err))
		}
	}
	if len(failures) > 0 {
		for _, f := range failures {
			log.Error().Err(f).Time("tick", at).Msg("cron tick: tenant failed")
		}
		return errors.Join(failures...)
	}
	return nil
}

func (w *Worker) insert(ctx context.Context, params []river.InsertManyParams) error {
	inserter := w.Inserter
	if inserter == nil {
		inserter = river.ClientFromContext[pgx.Tx](ctx)
	}
	_, err := inserter.InsertMany(ctx, params)
	return err
}

// dueEntries returns the enabled cron jobs of ready modules scheduled to fire
// at the minute at.
func dueEntries(snap *registry.RegistrySnapshot, at time.Time) []registry.CronEntry {
	var due []registry.CronEntry
	for _, entry := range snap.CronRegistry().Entries() {
		mod, ok := snap.Modules()[entry.Module]
		if !ok || mod.Status != module.StatusReady {
			continue
		}
		if !entry.Job.IsEnabledByDefault() {
			continue
		}
		if entry.Schedule.Next(at.Add(-time.Second)).Equal(at) {
			due = append(due, entry)
		}
	}
	return due
}

func cronJobParams(entry registry.CronEntry, tenantID string, at time.Time) river.InsertManyParams {
	return river.InsertManyParams{
		Args: jobqueue.WASMJobArgs{
			ModuleName:     entry.Module,
			JobType:        entry.Job.Name,
			TenantID:       tenantID,
			IsCron:         true,
			Queue:          entry.Job.EffectiveQueue(),
			MaxAttempts:    maxAttempts,
			IdempotencyKey: fmt.Sprintf("cron:%s:%s:%s", entry.Module, entry.Job.Name, at.Format(time.RFC3339)),
			TraceID:        uuid.NewV7().String(),
		},
		InsertOpts: &river.InsertOpts{
			// A fire time is owed once, even after its run completed or was discarded.
			UniqueOpts: river.UniqueOpts{ByArgs: true, ByState: jobqueue.UniqueAcrossAllJobStates},
		},
	}
}
