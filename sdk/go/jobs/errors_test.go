package jobs

import (
	"errors"
	"fmt"
	"testing"
	"time"
)

func TestPermanentError_DistinguishableViaErrorsAs(t *testing.T) {
	inner := fmt.Errorf("invalid credentials")
	err := PermanentError(inner)

	pe, ok := errors.AsType[*PermanentJobError](err)
	if !ok {
		t.Fatal("errors.AsType failed to find *PermanentJobError")
	}
	if pe.Err != inner {
		t.Fatalf("pe.Err = %v, want %v", pe.Err, inner)
	}
	if err.Error() != "invalid credentials" {
		t.Fatalf("Error() = %q, want the inner error's message", err.Error())
	}
}

func TestIsPermanentError(t *testing.T) {
	sentinel := errors.New("sentinel")
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"permanent", PermanentError(sentinel), true},
		{"wrapped permanent", fmt.Errorf("send: %w", PermanentError(sentinel)), true},
		{"plain", sentinel, false},
		{"retry after", RetryAfter(time.Minute, sentinel), false},
		{"nil", nil, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsPermanentError(tt.err); got != tt.want {
				t.Fatalf("IsPermanentError() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestRetryAfter_CarriesDelayAndUnwraps(t *testing.T) {
	sentinel := errors.New("rate limited")
	err := RetryAfter(5*time.Minute, sentinel)

	ra, ok := errors.AsType[*RetryAfterError](err)
	if !ok {
		t.Fatal("errors.AsType failed to find *RetryAfterError")
	}
	if ra.Delay != 5*time.Minute {
		t.Fatalf("ra.Delay = %v, want 5m", ra.Delay)
	}
	if !errors.Is(err, sentinel) {
		t.Fatal("errors.Is failed to find the wrapped error")
	}
}
