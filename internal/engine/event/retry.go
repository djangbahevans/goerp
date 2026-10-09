package event

import (
	"cmp"
	"fmt"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/manifest"
)

func retryPolicyFromManifest(policy *manifest.RetryPolicy) RetryPolicy {
	if policy == nil {
		return RetryPolicy{}
	}

	jitter := true
	if policy.Jitter != nil {
		jitter = *policy.Jitter
	}

	return RetryPolicy{
		MaxAttempts:  policy.MaxAttempts,
		Backoff:      policy.Backoff,
		InitialDelay: time.Duration(policy.InitialDelayMS) * time.Millisecond,
		MaxDelay:     cmp.Or(time.Duration(policy.MaxDelayMS)*time.Millisecond, defaultMaxDelay),
		Jitter:       jitter,
	}
}

// Delay is the upper bound before jitter. Saturating before multiplication keeps
// large declared delays from wrapping into immediate retries.
func (rp RetryPolicy) Delay(attempt int) time.Duration {
	attempt = max(1, attempt)
	maximumDelay := cmp.Or(rp.MaxDelay, time.Duration(1<<63-1))
	var factor time.Duration

	switch rp.Backoff {
	case "linear":
		factor = time.Duration(attempt)
	case "exponential":
		if attempt > 63 {
			return maximumDelay
		}
		factor = 1 << uint(attempt-1)
	default:
		return rp.InitialDelay
	}

	if rp.InitialDelay > maximumDelay/factor {
		return maximumDelay
	}

	return rp.InitialDelay * factor
}

func ValidateTransactionalSubscriptions(m manifest.Manifest, retention time.Duration) error {
	for _, sub := range m.Subscribes {
		if !sub.Transactional {
			continue
		}

		policy := retryPolicyFromManifest(sub.RetryPolicy)
		remaining := retention
		for attempt := 1; attempt <= policy.MaxAttempts; attempt++ {
			delay := policy.Delay(attempt)
			if delay > remaining {
				return fmt.Errorf("transactional subscription to event %q has a worst-case retry span exceeding GOERP_EVENT_LEDGER_RETENTION (%s)", sub.Name, retention)
			}
			remaining -= delay
		}
	}

	return nil
}
