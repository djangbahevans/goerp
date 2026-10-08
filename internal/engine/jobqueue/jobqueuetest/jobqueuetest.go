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

// New creates a queue client in an isolated test schema without periodic maintenance jobs.
// Call riverdbtest.TestSchema directly with DisableReuse to preserve caller-specific
// schema names and avoid cross-process truncation or cleanup races.
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
