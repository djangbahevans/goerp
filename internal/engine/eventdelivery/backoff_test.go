package eventdelivery

import (
	"testing"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/event"
)

func TestComputeBackoff_None(t *testing.T) {
	rp := event.RetryPolicy{Backoff: "none", InitialDelay: 5 * time.Second}
	got := time.Until(computeBackoff(rp, 1))
	if got < 4*time.Second || got > 6*time.Second {
		t.Fatalf("none backoff at attempt 1 = %v, want ~5s", got)
	}
	got = time.Until(computeBackoff(rp, 5))
	if got < 4*time.Second || got > 6*time.Second {
		t.Fatalf("none backoff at attempt 5 = %v, want ~5s (flat)", got)
	}
}

func TestComputeBackoff_Linear(t *testing.T) {
	rp := event.RetryPolicy{Backoff: "linear", InitialDelay: 2 * time.Second}
	for attempt, want := range map[int]time.Duration{1: 2 * time.Second, 3: 6 * time.Second} {
		got := time.Until(computeBackoff(rp, attempt))
		if diff := got - want; diff < -time.Second || diff > time.Second {
			t.Errorf("linear backoff at attempt %d = %v, want ~%v", attempt, got, want)
		}
	}
}

func TestComputeBackoff_LinearCappedAtMaxDelay(t *testing.T) {
	rp := event.RetryPolicy{Backoff: "linear", InitialDelay: 10 * time.Second, MaxDelay: 15 * time.Second}
	got := time.Until(computeBackoff(rp, 10))
	if got < 14*time.Second || got > 16*time.Second {
		t.Fatalf("linear backoff at attempt 10 = %v, want capped ~15s", got)
	}
}

func TestComputeBackoff_Exponential(t *testing.T) {
	rp := event.RetryPolicy{Backoff: "exponential", InitialDelay: time.Second}
	for attempt, want := range map[int]time.Duration{1: time.Second, 2: 2 * time.Second, 4: 8 * time.Second} {
		got := time.Until(computeBackoff(rp, attempt))
		if diff := got - want; diff < -time.Second || diff > time.Second {
			t.Errorf("exponential backoff at attempt %d = %v, want ~%v", attempt, got, want)
		}
	}
}

func TestComputeBackoff_ExponentialCappedAtMaxDelay(t *testing.T) {
	rp := event.RetryPolicy{Backoff: "exponential", InitialDelay: time.Second, MaxDelay: 10 * time.Second}
	got := time.Until(computeBackoff(rp, 10))
	if got < 9*time.Second || got > 11*time.Second {
		t.Fatalf("exponential backoff at attempt 10 = %v, want capped ~10s", got)
	}
}

func TestComputeBackoff_JitterStaysWithinEqualJitterRange(t *testing.T) {
	rp := event.RetryPolicy{Backoff: "linear", InitialDelay: 10 * time.Second, Jitter: true}
	for range 50 {
		got := time.Until(computeBackoff(rp, 1))
		// Equal jitter: [delay/2, delay), i.e. roughly [5s, 10s), with a
		// small tolerance for the time elapsed between Now() calls.
		if got < 4*time.Second || got > 11*time.Second {
			t.Fatalf("jittered backoff = %v, want within [5s, 10s)", got)
		}
	}
}

func TestComputeBackoff_AttemptBelowOneTreatedAsOne(t *testing.T) {
	rp := event.RetryPolicy{Backoff: "exponential", InitialDelay: time.Second}
	got0 := time.Until(computeBackoff(rp, 0))
	got1 := time.Until(computeBackoff(rp, 1))
	if diff := got0 - got1; diff < -500*time.Millisecond || diff > 500*time.Millisecond {
		t.Fatalf("attempt=0 (%v) should behave like attempt=1 (%v)", got0, got1)
	}
}
