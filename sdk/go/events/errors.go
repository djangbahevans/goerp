package events

import "time"

// PermanentDeliveryError marks a handler error as non-retryable — the
// future dispatch layer routes it straight to the dead-letter queue.
// Callers distinguish it from an ordinary error via errors.AsType,
// errors.As, or a type assertion.
type PermanentDeliveryError struct {
	Err error
}

func (e *PermanentDeliveryError) Error() string { return e.Err.Error() }
func (e *PermanentDeliveryError) Unwrap() error { return e.Err }

// PermanentError wraps err so a handler's return value is routed to the
// dead-letter queue immediately, without retry.
func PermanentError(err error) error {
	return &PermanentDeliveryError{Err: err}
}

// RetryAfterError marks a handler error as retryable no sooner than Delay.
type RetryAfterError struct {
	Err   error
	Delay time.Duration
}

func (e *RetryAfterError) Error() string { return e.Err.Error() }
func (e *RetryAfterError) Unwrap() error { return e.Err }

// RetryAfter wraps err so the future dispatch layer schedules a retry no
// sooner than d, instead of the subscription's ordinary retry policy.
func RetryAfter(d time.Duration, err error) error {
	return &RetryAfterError{Err: err, Delay: d}
}
