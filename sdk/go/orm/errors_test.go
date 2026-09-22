package orm

import (
	"errors"
	"fmt"
	"testing"

	abi "github.com/djangbahevans/goerp/contract/abi/v1"
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
		{"IsEtagMismatch", IsEtagMismatch, abi.ErrCodeEtagMismatch},
		{"IsPreconditionFailed", IsPreconditionFailed, abi.ErrCodePreconditionFailed},
		{"IsValidationFailed", IsValidationFailed, abi.ErrCodeValidationFailed},
		{"IsUniqueViolation", IsUniqueViolation, abi.ErrCodeUniqueViolation},
		{"IsFieldNotWritable", IsFieldNotWritable, abi.ErrCodeFieldNotWritable},
		{"IsFieldWriteDenied", IsFieldWriteDenied, abi.ErrCodeFieldWriteDenied},
		{"IsBatchTooLarge", IsBatchTooLarge, abi.ErrCodeBatchTooLarge},
		{"IsTimeout", IsTimeout, abi.ErrCodeORMTimeout},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			he := &abi.HostError{Code: c.code, Message: "boom"}
			if !c.matcher(he) {
				t.Errorf("%s(%s) = false, want true", c.name, c.code)
			}
			if !c.matcher(fmt.Errorf("wrap: %w", he)) {
				t.Errorf("%s(wrapped %s) = false, want true", c.name, c.code)
			}

			other := &abi.HostError{Code: abi.ErrCodeNotFound, Message: "record not found"}
			if c.matcher(other) {
				t.Errorf("%s(orm.not_found) = true, want false", c.name)
			}
		})
	}
}
