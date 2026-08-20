package events

import "time"

// PermanentErr marks a handler error as non-retryable — the future
// dispatch layer routes it straight to the dead-letter queue. Callers
// distinguish it from an ordinary error via errors.As or a type assertion.
type PermanentErr struct {
	Err error
}

func (e *PermanentErr) Error() string { return e.Err.Error() }
func (e *PermanentErr) Unwrap() error { return e.Err }

// PermanentError wraps err so a handler's return value is routed to the
// dead-letter queue immediately, without retry.
func PermanentError(err error) error {
	return &PermanentErr{Err: err}
}

// RetryAfterErr marks a handler error as retryable no sooner than Delay.
type RetryAfterErr struct {
	Err   error
	Delay time.Duration
}

func (e *RetryAfterErr) Error() string { return e.Err.Error() }
func (e *RetryAfterErr) Unwrap() error { return e.Err }

// RetryAfter wraps err so the future dispatch layer schedules a retry no
// sooner than d, instead of the subscription's ordinary retry policy.
func RetryAfter(d time.Duration, err error) error {
	return &RetryAfterErr{Err: err, Delay: d}
}
