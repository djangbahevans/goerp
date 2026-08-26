package eventdelivery

import (
	"math/rand/v2"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/event"
)

// computeBackoff translates a subscription's declared retry_policy
// (manifest-spec.md's RetryPolicy object) into the absolute time River
// should next attempt job — attempt is the 1-based attempt number the
// job is about to retry after (River's own river.Job.Attempt: 1 on the
// first retry following the initial try).
//
//   - "none": always InitialDelay after now, uncapped by MaxDelay (a flat
//     retry cadence has no curve to cap).
//   - "linear": InitialDelay*attempt, capped at MaxDelay.
//   - "exponential": InitialDelay*2^(attempt-1), capped at MaxDelay.
//
// When Jitter is set, the computed delay is replaced with a uniformly
// random duration in [delay/2, delay) — AWS's "equal jitter" recipe:
// preserves roughly half the intended backoff (so retries still spread
// out over time) while breaking the exact-same-instant thundering-herd
// case a fixed delay would produce across many subscribers retrying the
// same failed event together.
func computeBackoff(rp event.RetryPolicy, attempt int) time.Time {
	if attempt < 1 {
		attempt = 1
	}

	var delay time.Duration
	switch rp.Backoff {
	case "linear":
		delay = rp.InitialDelay * time.Duration(attempt)
		if rp.MaxDelay > 0 && delay > rp.MaxDelay {
			delay = rp.MaxDelay
		}
	case "exponential":
		delay = rp.InitialDelay * time.Duration(1<<uint(attempt-1))
		if rp.MaxDelay > 0 && delay > rp.MaxDelay {
			delay = rp.MaxDelay
		}
	default: // "none", or an unrecognized value — treat as a flat retry cadence
		delay = rp.InitialDelay
	}

	if rp.Jitter && delay > 0 {
		half := delay / 2
		delay = half + time.Duration(rand.Int64N(int64(half)+1))
	}

	return time.Now().Add(delay)
}
