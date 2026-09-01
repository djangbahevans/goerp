package db

import (
	"errors"
	"fmt"
	"testing"
)

func TestIsLockTimeout(t *testing.T) {
	wrapped := fmt.Errorf("db: lock %q: %w", "some-key", ErrLockTimeout)
	if !IsLockTimeout(wrapped) {
		t.Error("IsLockTimeout(wrapped ErrLockTimeout) = false, want true")
	}
	if IsLockTimeout(ErrNotFound) {
		t.Error("IsLockTimeout(ErrNotFound) = true, want false")
	}
	if IsLockTimeout(errors.New("unrelated")) {
		t.Error("IsLockTimeout(unrelated error) = true, want false")
	}
}
