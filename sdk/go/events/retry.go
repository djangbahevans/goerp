package events

import "time"

// Backoff is a retry delay strategy; its values are the manifest's
// retry_policy.backoff strings.
type Backoff string

const (
	NoBackoff   Backoff = "none"
	Linear      Backoff = "linear"
	Exponential Backoff = "exponential"
)

// RetryPolicy is a subscription's retry configuration, written to the
// manifest's retry_policy.
type RetryPolicy struct {
	// MaxAttempts is manifest max_attempts, 1–25.
	MaxAttempts int
	Backoff     Backoff
	// InitialDelay is manifest initial_delay_ms, at least 100ms.
	InitialDelay time.Duration
	// MaxDelay is manifest max_delay_ms; zero keeps the manifest default (24h).
	MaxDelay time.Duration
	// NoJitter true writes manifest jitter: false; jitter is on by default.
	NoJitter bool
}
