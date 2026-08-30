// Package jobqueue is the engine's River-backed background job queue
// (erp-design.md §7.1): 5 named queues, per-queue concurrency, and the
// idempotency-key convention job types should follow (see ProbeArgs).
// Wiring into engine.New and real job types belongs to later tickets.
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
	"github.com/riverqueue/river/riverdbtest"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/riverqueue/river/rivermigrate"
	"github.com/riverqueue/river/rivershared/util/testutil"
)

// Queue names from erp-design.md §7.1.
const (
	QueueCritical = "critical"
	QueueDefault  = "default"
	QueueBulk     = "bulk"
	QueueSearch   = "search"
	QueueEmail    = "email"
	// QueueAdmin is the reserved queue for admin API async operations
	// (engine-internals.md §11a) — module install/downgrade, schema
	// sync/accept/drop-index, tenant export/import, and immediate tenant
	// offboard. Distinct from the 5 business queues above so an admin
	// operator's own long-running request never contends with ordinary
	// tenant job throughput.
	QueueAdmin = "admin"
	// QueueEvents carries EventDelivery jobs inserted by host.event.emit_tx
	// (engine-internals.md §9) — separate from the 5 business queues above
	// so event fan-out throughput never contends with them.
	QueueEvents = "events"
)

// migrateLockKey serializes concurrent Migrate callers against the same
// pool — see Migrate's own doc comment for why this is needed.
var migrateLockKey = db.AdvisoryLockKey("jobqueue.Migrate")

// Migrate applies River's own schema migrations against pool. Idempotent —
// safe to call on every startup, same as Store.Bootstrap elsewhere — but
// unlike Store.Bootstrap/SchemaSyncPool.Bootstrap, River's own migration
// DDL (CREATE TYPE/CREATE FUNCTION, no IF NOT EXISTS guard) isn't safe
// against two callers running it at the same time: two concurrent test
// packages both calling Migrate against the same dev Postgres instance hit
// "duplicate key value violates unique constraint" on Postgres's own
// pg_type/pg_proc catalogs. Guarded the same way goerp#171 guarded
// Store.Bootstrap — a transaction-scoped Postgres advisory lock
// (pg_advisory_xact_lock) held for the duration of the migration, so a
// second concurrent caller blocks until the first commits or rolls back
// rather than racing it.
//
// The lock is held on its own connection, opened directly rather than
// acquired from pool. Acquiring it from pool instead would leave that
// connection idle for the whole migration while riverpgxv5.New(pool) draws
// its own connections from the same pool to actually run it — on a small
// pool (a constrained CI runner, a caller-supplied pool sized for one
// purpose) that idle reservation can starve the migration of a connection
// to run on at all, deadlocking until the caller's own timeout kills it. A
// connection opened outside pool's own accounting can't compete with pool
// for capacity.
func Migrate(ctx context.Context, pool *pgxpool.Pool) error {
	lockConn, err := pgx.ConnectConfig(ctx, pool.Config().ConnConfig.Copy())
	if err != nil {
		return fmt.Errorf("open migration lock connection: %w", err)
	}
	defer func() { _ = lockConn.Close(ctx) }()

	tx, err := lockConn.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin migration lock transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock($1)", migrateLockKey); err != nil {
		return fmt.Errorf("acquire migration advisory lock: %w", err)
	}

	migrator, err := rivermigrate.New(riverpgxv5.New(pool), nil)
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
		Queues:  queueConfig(cfg),
		Workers: workers,
		// Platform-wide (not per-tenant), so a single hourly job — not one
		// per tenant — per goerp#194/data-layer.md §2.6: pg_partman's own
		// run_maintenance() already iterates every table any tenant schema
		// has registered with it.
		PeriodicJobs: []*river.PeriodicJob{
			river.NewPeriodicJob(
				river.PeriodicInterval(time.Hour),
				func() (river.JobArgs, *river.InsertOpts) {
					return PartitionMaintenanceArgs{}, nil
				},
				&river.PeriodicJobOpts{RunOnStart: false},
			),
			// Platform-wide (not per-tenant) — a single run fans out across
			// every active tenant itself (goerp#163), the same shape as the
			// periodic job above.
			river.NewPeriodicJob(
				river.PeriodicInterval(time.Hour),
				func() (river.JobArgs, *river.InsertOpts) {
					return InviteExpiryArgs{}, nil
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

// NewIsolated is New, but for tests: it builds the client against a
// freshly migrated, uniquely-named Postgres schema (via
// riverdbtest.TestSchema, River's own first-party test-isolation helper)
// instead of River's default schema, and without the two hourly
// PeriodicJobs (platform-wide maintenance work no test needs running
// alongside it). New's own Queues map above uses fixed, package-level
// constant names (QueueAdmin, QueueDefault, etc.) — identical across every
// caller, with no per-caller scoping — and River's own job-claim query has
// no scoping beyond schema either, so two River clients built with New
// against the same Postgres database, in two different test packages
// running concurrently against the same shared dev Postgres, poll and can
// claim jobs off each other's queues. A client whose own Workers doesn't
// know the claimed job's kind logs "Unhandled job kind" and leaves it
// retryable — indistinguishable, from the test that actually enqueued it,
// from that job simply never running. NewIsolated's own Schema sidesteps
// this entirely: each call gets a schema no other client is polling.
//
// tb only needs a Cleanup method (satisfied by *testing.T, among others) —
// riverdbtest checks the schema back into its own reuse pool automatically
// once the test that requested it finishes.
func NewIsolated(ctx context.Context, tb testutil.TestingTB, pool *pgxpool.Pool, cfg *config.Config, workers *river.Workers) (*river.Client[pgx.Tx], error) {
	driver := riverpgxv5.New(pool)
	schema := riverdbtest.TestSchema(ctx, tb, driver, nil)

	client, err := river.NewClient(driver, &river.Config{
		Schema:  schema,
		Queues:  queueConfig(cfg),
		Workers: workers,
	})
	if err != nil {
		return nil, fmt.Errorf("create isolated river client: %w", err)
	}
	return client, nil
}

// queueConfig builds New/NewIsolated's shared Queues map — all 5 business
// queues plus the 2 platform-reserved ones, each sized from cfg.
func queueConfig(cfg *config.Config) map[string]river.QueueConfig {
	return map[string]river.QueueConfig{
		QueueCritical: {MaxWorkers: withDefault(cfg.QueueCriticalConcurrency, 5)},
		QueueDefault:  {MaxWorkers: withDefault(cfg.QueueDefaultConcurrency, 10)},
		QueueBulk:     {MaxWorkers: withDefault(cfg.QueueBulkConcurrency, 20)},
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
