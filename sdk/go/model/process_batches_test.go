package model

import (
	"errors"
	"testing"

	abi "github.com/djangbahevans/goerp/contract/abi/v1"
)

func TestIsRetryable_HostErrorWithRetryTrue(t *testing.T) {
	err := &abi.HostError{Code: abi.ErrCodeDBTimeout, Retry: true}
	if !isRetryable(err) {
		t.Error("isRetryable() = false, want true for a HostError with Retry: true")
	}
}

func TestIsRetryable_HostErrorWithRetryFalse(t *testing.T) {
	err := &abi.HostError{Code: abi.ErrCodeQueryError, Retry: false}
	if isRetryable(err) {
		t.Error("isRetryable() = true, want false for a HostError with Retry: false")
	}
}

func TestIsRetryable_PlainErrorIsNotRetryable(t *testing.T) {
	if isRetryable(errors.New("boom")) {
		t.Error("isRetryable() = true, want false for a plain error")
	}
}
