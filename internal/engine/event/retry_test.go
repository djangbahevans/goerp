package event

import (
	"testing"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/manifest"
)

func TestRetryDelay_LargeDelaysRemainCapped(t *testing.T) {
	for _, backoff := range []string{"linear", "exponential"} {
		policy := RetryPolicy{Backoff: backoff, InitialDelay: time.Duration(1 << 62), MaxDelay: time.Hour}
		for _, attempt := range []int{1, 25, 64} {
			if got := policy.Delay(attempt); got != time.Hour {
				t.Fatalf("%s delay at attempt %d = %s, want 1h", backoff, attempt, got)
			}
		}
	}
}

func TestTransactionalRetention_DefaultCap(t *testing.T) {
	m := manifest.Manifest{
		Subscribes: []manifest.EventSubscription{
			{
				Name: "demo.order.created", Async: true, Transactional: true,
				RetryPolicy: &manifest.RetryPolicy{MaxAttempts: 25, Backoff: "exponential", InitialDelayMS: 1000},
			},
		},
	}

	if err := ValidateTransactionalSubscriptions(m, 840*time.Hour); err != nil {
		t.Fatalf("default retention must cover a default-capped retry policy: %v", err)
	}
	if err := ValidateTransactionalSubscriptions(m, time.Hour); err == nil {
		t.Fatal("default-capped policy exceeded retention without a load error")
	}
}
