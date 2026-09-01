package orm

import (
	"errors"
	"fmt"
	"testing"

	"github.com/djangbahevans/goerp/sdk/go/internal/hostcall"
)

func TestIsNotFound_MatchesErrNotFound(t *testing.T) {
	if !IsNotFound(ErrNotFound) {
		t.Error("IsNotFound(ErrNotFound) = false, want true")
	}
	if !IsNotFound(fmt.Errorf("wrap: %w", ErrNotFound)) {
		t.Error("IsNotFound(wrapped ErrNotFound) = false, want true")
	}
	if IsNotFound(errors.New("some other error")) {
		t.Error("IsNotFound(unrelated error) = true, want false")
	}
}

func TestIsEtagMismatch_MatchesHostErrorCode(t *testing.T) {
	he := &hostcall.HostError{Code: "orm.etag_mismatch", Message: "record has been modified since it was last read"}
	if !IsEtagMismatch(he) {
		t.Error("IsEtagMismatch(orm.etag_mismatch) = false, want true")
	}

	other := &hostcall.HostError{Code: "orm.not_found", Message: "record not found"}
	if IsEtagMismatch(other) {
		t.Error("IsEtagMismatch(orm.not_found) = true, want false")
	}
}
