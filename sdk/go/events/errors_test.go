package events

import (
	"errors"
	"fmt"
	"testing"
	"time"
)

func TestPermanentError_DistinguishableViaErrorsAs(t *testing.T) {
	inner := fmt.Errorf("malformed payload")
	err := PermanentError(inner)

	pe, ok := errors.AsType[*PermanentErr](err)
	if !ok {
		t.Fatal("errors.AsType failed to find *PermanentErr")
	}
	if pe.Err != inner {
		t.Fatalf("pe.Err = %v, want %v", pe.Err, inner)
	}
}

func TestPermanentError_ErrorStringDelegatesToInner(t *testing.T) {
	inner := fmt.Errorf("boom")
	err := PermanentError(inner)
	if err.Error() != "boom" {
		t.Fatalf("Error() = %q, want %q", err.Error(), "boom")
	}
}

func TestRetryAfter_DistinguishableViaErrorsAs(t *testing.T) {
	inner := fmt.Errorf("rate limited")
	err := RetryAfter(5*time.Minute, inner)

	ra, ok := errors.AsType[*RetryAfterErr](err)
	if !ok {
		t.Fatal("errors.AsType failed to find *RetryAfterErr")
	}
	if ra.Delay != 5*time.Minute {
		t.Fatalf("ra.Delay = %v, want 5m", ra.Delay)
	}
	if ra.Err != inner {
		t.Fatalf("ra.Err = %v, want %v", ra.Err, inner)
	}
}

func TestPlainError_IsNotPermanentOrRetryAfter(t *testing.T) {
	err := fmt.Errorf("transient database error")

	if _, ok := errors.AsType[*PermanentErr](err); ok {
		t.Error("plain error matched *PermanentErr, want no match")
	}
	if _, ok := errors.AsType[*RetryAfterErr](err); ok {
		t.Error("plain error matched *RetryAfterErr, want no match")
	}
}

func TestPermanentError_UnwrapsForErrorsIs(t *testing.T) {
	sentinel := errors.New("sentinel")
	wrapped := fmt.Errorf("context: %w", sentinel)
	err := PermanentError(wrapped)

	if !errors.Is(err, sentinel) {
		t.Fatal("errors.Is failed to find sentinel through PermanentErr")
	}
}
