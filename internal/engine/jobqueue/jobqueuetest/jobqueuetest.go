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
	"fmt"

	"github.com/djangbahevans/goerp/internal/engine/config"
	"github.com/djangbahevans/goerp/internal/engine/jobqueue"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver"
)

// New is jobqueue.New, but for tests: it builds the client against
// schema — a schema isolated to the calling test, expected to already be
// migrated via riverdbtest.TestSchema — instead of River's default
// schema, and without jobqueue.New's two hourly PeriodicJobs
// (platform-wide maintenance work no test needs running alongside it).
// jobqueue.New's own Queues map uses fixed, package-level constant names
// (jobqueue.QueueAdmin, jobqueue.QueueDefault, etc.) — identical across
// every caller, with no per-caller scoping — and River's own job-claim
// query has no scoping beyond schema either, so two River clients built
// with jobqueue.New against the same Postgres database, in two different
// test packages running concurrently against the same shared dev
// Postgres, poll and can claim jobs off each other's queues. A client
// whose own Workers doesn't know the claimed job's kind logs "Unhandled
// job kind" and leaves it retryable — indistinguishable, from the test
// that actually enqueued it, from that job simply never running. A
// schema no other client is polling sidesteps this entirely.
//
// schema must come from the caller's own direct call to
// riverdbtest.TestSchema — not a call routed through this package or any
// other intermediary. riverdbtest.TestSchema derives its own schema-name
// prefix by walking the call stack to the immediate caller's package; a
// wrapper here would always be that immediate caller instead of whichever
// real package is actually running the test, collapsing every caller
// across every package into one shared "jobqueuetest"-prefixed
// schema-name namespace. Two callers in two different, concurrently-
// running test binaries could then compute the identical schema name
// within the same second, and one process's own startup routine (which
// drops schemas it considers left over from a previous run) can drop a
// schema a *different*, still-running process is actively using right
// now — a schema or its river_job/river_leader tables disappearing out
// from under a live test (goerp#526). Pass
// &riverdbtest.TestSchemaOpts{DisableReuse: true} at the call site too:
// disabling reuse doesn't fix the naming collision itself, but it does
// remove the specific window (a schema checked in by one test, handed
// back out and TableTruncate'd for another) where that collision was
// observed turning into this failure in practice.
func New(driver riverdriver.Driver[pgx.Tx], schema string, cfg *config.Config, workers *river.Workers) (*river.Client[pgx.Tx], error) {
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
