package eventdelivery

import (
	"math/rand/v2"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/event"
)

func computeBackoff(rp event.RetryPolicy, attempt int) time.Time {
	delay := rp.Delay(attempt)

	if rp.Jitter && delay > 0 {
		half := delay / 2
		delay = half + time.Duration(rand.Int64N(int64(half)+1))
	}

	return time.Now().Add(delay)
}
