// Package jobqueue configures the engine's River queues, workers, periodic jobs, and
// migrations.
package jobqueue

import (
	"context"
	"fmt"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/config"
	"github.com/djangbahevans/goerp/internal/engine/db"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/riverqueue/river/rivermigrate"
)

const (
	QueueCritical = "critical"
	QueueDefault  = "default"
	QueueBulk     = "bulk"
	QueueSearch   = "search"
	QueueEmail    = "email"
	// QueueAdmin isolates long-running administrative operations from tenant business
	// jobs.
	QueueAdmin = "admin"
	// QueueEvents isolates event fan-out from tenant business jobs.
	QueueEvents = "events"
)

// Schema holds River's tables, off every module transaction's search_path.
const Schema = "system"

var migrateLockKey = db.AdvisoryLockKey("jobqueue.Migrate")

// Migrate serializes River schema migrations with an advisory lock. The lock uses a
// separate connection so reserving it cannot exhaust the migration pool and deadlock a
// small pool.
func Migrate(ctx context.Context, pool *pgxpool.Pool) error {
	lockConn, err := pgx.ConnectConfig(ctx, pool.Config().ConnConfig.Copy())
	if err != nil {
		return fmt.Errorf("open migration lock connection: %w", err)
	}
	defer func() { _ = lockConn.Close(ctx) }()

	// Committed on its own so the migrator's pool connections see it.
	if err := pgx.BeginFunc(ctx, lockConn, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock($1)", db.SystemSchemaLockKey); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, "CREATE SCHEMA IF NOT EXISTS "+Schema)
		return err
	}); err != nil {
		return fmt.Errorf("create job queue schema: %w", err)
	}

	tx, err := lockConn.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin migration lock transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock($1)", migrateLockKey); err != nil {
		return fmt.Errorf("acquire migration advisory lock: %w", err)
	}

	migrator, err := rivermigrate.New(riverpgxv5.New(pool), &rivermigrate.Config{Schema: Schema})
	if err != nil {
		return fmt.Errorf("create river migrator: %w", err)
	}
	if _, err := migrator.Migrate(ctx, rivermigrate.DirectionUp, nil); err != nil {
		return fmt.Errorf("apply river migrations: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit migration lock transaction: %w", err)
	}

	return nil
}

// New builds a River client against pool with all 5 queues and their
// per-queue concurrency from cfg. workers must be registered for every job
// type the client should work (river.NewWorkers + river.AddWorker), or
// left empty for an insert-only client that never calls Start.
//
// pool is expected to be PgBouncer-pooled (like Store.Bootstrap elsewhere),
// so River falls back to polling rather than LISTEN/NOTIFY — a latency
// tradeoff, not a correctness one.
func New(pool *pgxpool.Pool, cfg *config.Config, workers *river.Workers) (*river.Client[pgx.Tx], error) {
	client, err := river.NewClient(riverpgxv5.New(pool), &river.Config{
		Schema:            Schema,
		Queues:            QueueConfig(cfg),
		Workers:           workers,
		ReindexerSchedule: river.NeverSchedule(),
		// One platform-wide maintenance job covers all registered partitioned tables.
		PeriodicJobs: []*river.PeriodicJob{
			river.NewPeriodicJob(
				river.PeriodicInterval(time.Hour),
				func() (river.JobArgs, *river.InsertOpts) {
					return PartitionMaintenanceArgs{}, nil
				},
				&river.PeriodicJobOpts{RunOnStart: false},
			),
			river.NewPeriodicJob(
				river.PeriodicInterval(time.Hour),
				func() (river.JobArgs, *river.InsertOpts) {
					return InviteExpiryArgs{}, nil
				},
				&river.PeriodicJobOpts{RunOnStart: false},
			),
			// RunOnStart prevents frequent engine restarts from indefinitely postponing
			// this periodic sweep.
			river.NewPeriodicJob(
				river.PeriodicInterval(ActivityDueInterval),
				func() (river.JobArgs, *river.InsertOpts) {
					return ActivityDueArgs{}, nil
				},
				&river.PeriodicJobOpts{RunOnStart: true},
			),
			river.NewPeriodicJob(
				river.PeriodicInterval(24*time.Hour),
				func() (river.JobArgs, *river.InsertOpts) {
					return ReindexArgs{}, nil
				},
				&river.PeriodicJobOpts{RunOnStart: false},
			),
			// RunOnStart prevents frequent engine restarts from indefinitely postponing
			// this daily sweep; repeated runs are idempotent.
			river.NewPeriodicJob(
				river.PeriodicInterval(24*time.Hour),
				func() (river.JobArgs, *river.InsertOpts) {
					return DeviceTokenCleanupArgs{}, nil
				},
				&river.PeriodicJobOpts{RunOnStart: true},
			),
			// Fans module cron jobs out to the active tenants that have the
			// module enabled. Never RunOnStart: a restarted engine does not
			// replay the minute it missed, since a cron job is not owed a
			// catch-up run.
			river.NewPeriodicJob(
				minuteSchedule{},
				func() (river.JobArgs, *river.InsertOpts) {
					return CronTickArgs{At: time.Now().UTC().Truncate(time.Minute)}, nil
				},
				&river.PeriodicJobOpts{RunOnStart: false},
			),
		},
	})
	if err != nil {
		return nil, fmt.Errorf("create river client: %w", err)
	}
	return client, nil
}

// QueueConfig builds New's Queues map — all 5 business queues plus the 2
// platform-reserved ones, each sized from cfg. Exported so
// internal/engine/jobqueue/jobqueuetest can build an identical Queues map
// for its own isolated test clients without duplicating it.
func QueueConfig(cfg *config.Config) map[string]river.QueueConfig {
	return map[string]river.QueueConfig{
		QueueCritical: {MaxWorkers: withDefault(cfg.QueueCriticalConcurrency, 5)},
		QueueDefault:  {MaxWorkers: withDefault(cfg.QueueDefaultConcurrency, 10)},
		QueueBulk:     {MaxWorkers: withDefault(cfg.QueueBulkConcurrency, 3)},
		QueueSearch:   {MaxWorkers: withDefault(cfg.QueueSearchConcurrency, 5)},
		QueueEmail:    {MaxWorkers: withDefault(cfg.QueueEmailConcurrency, 5)},
		QueueAdmin:    {MaxWorkers: withDefault(cfg.QueueAdminConcurrency, 5)},
		QueueEvents:   {MaxWorkers: withDefault(cfg.QueueEventsConcurrency, 10)},
	}
}

// withDefault mirrors config.go's own envDefault for each field — needed
// because River requires MaxWorkers >= 1, and a Config built by hand
// (rather than via config.Load) leaves unset fields at zero.
func withDefault(n, def int) int {
	if n <= 0 {
		return def
	}
	return n
}
