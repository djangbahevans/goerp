// Package jobqueuetest is jobqueue's test-only companion, the same
// arrangement as the standard library's net/http/httptest or
// testing/iotest: a separate, non-_test.go but clearly test-scoped
// package, so riverdbtest/testify (and their own further dependencies)
// stay out of the real jobqueue package's production dependency graph —
// linked into cmd/engine — even though New here needs to be callable from
// other packages' test files, which rules out a _test.go file (those
// aren't importable across packages).
package jobqueuetest

import (
	"context"
	"fmt"

	"github.com/djangbahevans/goerp/internal/engine/config"
	"github.com/djangbahevans/goerp/internal/engine/jobqueue"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdbtest"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/riverqueue/river/rivershared/util/testutil"
)

// New is jobqueue.New, but for tests: it builds the client against a
// freshly migrated, uniquely-named Postgres schema (via
// riverdbtest.TestSchema, River's own first-party test-isolation helper)
// instead of River's default schema, and without jobqueue.New's two
// hourly PeriodicJobs (platform-wide maintenance work no test needs
// running alongside it). jobqueue.New's own Queues map uses fixed,
// package-level constant names (jobqueue.QueueAdmin, jobqueue.QueueDefault,
// etc.) — identical across every caller, with no per-caller scoping — and
// River's own job-claim query has no scoping beyond schema either, so two
// River clients built with jobqueue.New against the same Postgres
// database, in two different test packages running concurrently against
// the same shared dev Postgres, poll and can claim jobs off each other's
// queues. A client whose own Workers doesn't know the claimed job's kind
// logs "Unhandled job kind" and leaves it retryable — indistinguishable,
// from the test that actually enqueued it, from that job simply never
// running. New's own Schema sidesteps this entirely: each call gets a
// schema no other client is polling.
//
// tb only needs a Cleanup method (satisfied by *testing.T, among others) —
// riverdbtest checks the schema back into its own reuse pool automatically
// once the test that requested it finishes.
func New(ctx context.Context, tb testutil.TestingTB, pool *pgxpool.Pool, cfg *config.Config, workers *river.Workers) (*river.Client[pgx.Tx], error) {
	driver := riverpgxv5.New(pool)
	schema := riverdbtest.TestSchema(ctx, tb, driver, nil)

	client, err := river.NewClient(driver, &river.Config{
		Schema:  schema,
		Queues:  jobqueue.QueueConfig(cfg),
		Workers: workers,
	})
	if err != nil {
		return nil, fmt.Errorf("create isolated river client: %w", err)
	}
	return client, nil
}
