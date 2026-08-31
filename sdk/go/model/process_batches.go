package model

import (
	"fmt"
	"time"

	"github.com/djangbahevans/goerp/sdk/go/db"
	"github.com/djangbahevans/goerp/sdk/go/internal/hostcall"
)

// processBatchesMaxRetries is migration-guide.md §4's own documented
// retry count for a batch that fails on a transient error.
const processBatchesMaxRetries = 3

// processBatchesRetryBackoff is the delay before each retry — a fixed,
// short backoff (not exponential: 3 attempts total doesn't leave enough
// retries for a growing delay to matter) so a retried db.timeout doesn't
// immediately re-hit whatever transient condition (lock contention, a
// brief connection blip) caused the first failure.
const processBatchesRetryBackoff = 200 * time.Millisecond

// ProcessBatches repeatedly queries table for rows matching condition, in
// batches of batchSize, calling fn once per batch — until a query
// returns no rows. No OFFSET/cursor is needed: condition is expected to
// describe "still needs processing" (migration-guide.md §4's own
// example, "display_name IS NULL"), so once fn's own writes to a batch's
// rows take effect, those rows stop matching condition and the same
// LIMIT query naturally advances to the next batch — the same property
// that makes this safe to resume after an interruption. A batch is
// retried up to processBatchesMaxRetries times only when the failure is
// itself flagged retryable (*hostcall.HostError.Retry, e.g. db.timeout) —
// anything else stops immediately, since retrying a non-transient error
// (a malformed condition, a fn bug) would just fail identically each
// time.
//
// migration-guide.md §4 documents "each batch runs in its own
// transaction (short lock windows)" — not implemented here: fn's own
// documented signature (func(batch []map[string]any) error) has no way
// to receive a transaction handle, and host.db.exec (the write path a
// real fn would call, goerp#460) doesn't exist yet either. Opening a
// db.Begin a caller's fn has no reference to would commit or roll back
// nothing fn actually did, which is worse than not pretending to —
// wrapping this correctly needs host.db.exec's own design (goerp#460) to
// settle how a handler joins a transaction it didn't open itself, and is
// deferred to whatever ticket wires fn's own writes through it.
//
// ctx is accepted, matching migration-guide.md §4's own documented
// signature, but not used here: both of the guide's own worked
// examples call ctx.RecordProgress(1) themselves inside fn's own
// per-row loop, at finer granularity than a per-batch count this
// function could report on fn's behalf — reporting again here would
// double-count.
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

// withRetry calls fn, retrying up to processBatchesMaxRetries times, each
// after processBatchesRetryBackoff, but only while the failure is itself
// flagged retryable (isRetryable) — the one retry policy queryBatch and
// ProcessBatches' own per-batch fn call both need.
func withRetry(fn func() error) error {
	var err error
	for attempt := 0; ; attempt++ {
		err = fn()
		if err == nil {
			return nil
		}
		if attempt >= processBatchesMaxRetries-1 || !isRetryable(err) {
			return err
		}
		time.Sleep(processBatchesRetryBackoff)
	}
}

func isRetryable(err error) bool {
	hostErr, ok := err.(*hostcall.HostError)
	return ok && hostErr.Retry
}
