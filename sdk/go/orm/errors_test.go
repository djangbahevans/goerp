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

func TestHostErrorCodeMatchers_MatchTheirOwnCodeOnly(t *testing.T) {
	cases := []struct {
		name    string
		matcher func(error) bool
		code    string
	}{
		{"IsEtagMismatch", IsEtagMismatch, "orm.etag_mismatch"},
		{"IsValidationFailed", IsValidationFailed, "orm.validation_failed"},
		{"IsUniqueViolation", IsUniqueViolation, "orm.unique_violation"},
		{"IsFieldNotWritable", IsFieldNotWritable, "orm.field_not_writable"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			he := &hostcall.HostError{Code: c.code, Message: "boom"}
			if !c.matcher(he) {
				t.Errorf("%s(%s) = false, want true", c.name, c.code)
			}
			if !c.matcher(fmt.Errorf("wrap: %w", he)) {
				t.Errorf("%s(wrapped %s) = false, want true", c.name, c.code)
			}

			other := &hostcall.HostError{Code: "orm.not_found", Message: "record not found"}
			if c.matcher(other) {
				t.Errorf("%s(orm.not_found) = true, want false", c.name)
			}
		})
	}
}
