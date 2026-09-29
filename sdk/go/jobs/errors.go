package jobs

import (
	"errors"
	"time"
)

// PermanentJobError marks a job handler error as non-retryable: the engine
// cancels the job instead of retrying it. Callers distinguish it from an
// ordinary error via IsPermanentError, errors.AsType, errors.As, or a type
// assertion.
type PermanentJobError struct {
	Err error
}

func (e *PermanentJobError) Error() string { return e.Err.Error() }
func (e *PermanentJobError) Unwrap() error { return e.Err }

// PermanentError wraps err so a job handler's return value stops retries
// immediately.
func PermanentError(err error) error {
	return &PermanentJobError{Err: err}
}

// IsPermanentError reports whether err, or any error it wraps, was
// returned by PermanentError.
func IsPermanentError(err error) bool {
	_, ok := errors.AsType[*PermanentJobError](err)
	return ok
}

// RetryAfterError marks a job handler error as retryable no sooner than
// Delay.
type RetryAfterError struct {
	Err   error
	Delay time.Duration
}

func (e *RetryAfterError) Error() string { return e.Err.Error() }
func (e *RetryAfterError) Unwrap() error { return e.Err }

// RetryAfter wraps err so the job is retried. handle_job's status code
// carries no delay, so the retry follows the job's ordinary backoff rather
// than d, the same as events.RetryAfter.
func RetryAfter(d time.Duration, err error) error {
	return &RetryAfterError{Err: err, Delay: d}
}
