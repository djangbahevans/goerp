package model

import (
	"errors"
	"testing"

	"github.com/djangbahevans/goerp/sdk/go/internal/hostcall"
)

func TestIsRetryable_HostErrorWithRetryTrue(t *testing.T) {
	err := &hostcall.HostError{Code: "db.timeout", Retry: true}
	if !isRetryable(err) {
		t.Error("isRetryable() = false, want true for a HostError with Retry: true")
	}
}

func TestIsRetryable_HostErrorWithRetryFalse(t *testing.T) {
	err := &hostcall.HostError{Code: "db.query_error", Retry: false}
	if isRetryable(err) {
		t.Error("isRetryable() = true, want false for a HostError with Retry: false")
	}
}

func TestIsRetryable_PlainErrorIsNotRetryable(t *testing.T) {
	if isRetryable(errors.New("boom")) {
		t.Error("isRetryable() = true, want false for a plain error")
	}
}
