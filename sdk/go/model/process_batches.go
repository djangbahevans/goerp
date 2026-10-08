package model

import (
	"errors"
	"fmt"
	"time"

	abi "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/djangbahevans/goerp/sdk/go/db"
)

// processBatchesMaxAttempts caps how often a batch failing on a transient
// error is attempted.
const processBatchesMaxAttempts = 3

// processBatchesRetryBackoff is the fixed delay before each retry; with so
// few retries an exponential backoff would not help.
const processBatchesRetryBackoff = 200 * time.Millisecond

// ProcessBatches repeatedly queries table for rows matching condition, in
// batches of batchSize, calling fn once per batch until a query returns no
// rows. condition must describe rows that still need processing (for
// example "display_name IS NULL"), so that fn's writes make processed rows
// stop matching; this is what advances to the next batch and makes the
// migration safe to resume. A batch is attempted up to three times when
// the failure is retryable, such as db.timeout; any other failure stops
// immediately.
//
// fn's writes are not wrapped in a per-batch transaction. ProcessBatches
// does not report progress itself; call ctx.RecordProgress from fn.
func ProcessBatches(ctx *MigrationContext, table, condition string, batchSize int, fn func(batch []map[string]any) error) error {
	_ = ctx
	for {
		batch, err := queryBatch(table, condition, batchSize)
		if err != nil {
			return err
		}
		if len(batch) == 0 {
			return nil
		}

		if err := withRetry(func() error { return fn(batch) }); err != nil {
			return err
		}
	}
}

func queryBatch(table, condition string, batchSize int) ([]map[string]any, error) {
	sql := fmt.Sprintf("SELECT * FROM %s WHERE %s LIMIT %d", table, condition, batchSize)

	var result *db.QueryResult
	err := withRetry(func() error {
		var queryErr error
		result, queryErr = db.QueryRaw(sql, nil)
		return queryErr
	})
	if err != nil {
		return nil, fmt.Errorf("query batch from %s: %w", table, err)
	}

	return result.AsMaps(), nil
}

// withRetry calls fn up to processBatchesMaxAttempts times, retrying only
// retryable failures, after processBatchesRetryBackoff each.
func withRetry(fn func() error) error {
	var err error
	for attempt := 0; ; attempt++ {
		err = fn()
		if err == nil {
			return nil
		}
		if attempt >= processBatchesMaxAttempts-1 || !isRetryable(err) {
			return err
		}
		time.Sleep(processBatchesRetryBackoff)
	}
}

func isRetryable(err error) bool {
	hostErr, ok := errors.AsType[*abi.HostError](err)
	return ok && hostErr.Retry
}
