package clierr

import (
	"errors"
	"testing"

	"github.com/spf13/cobra"
)

func TestErrorImplementsExitCoder(t *testing.T) {
	var _ ExitCoder = (*Error)(nil)
}

func TestErrorMethods(t *testing.T) {
	inner := errors.New("boom")
	err := &Error{Code: 4, Err: inner}

	if got := err.Error(); got != "boom" {
		t.Errorf("Error() = %q, want %q", got, "boom")
	}
	if got := err.ExitCode(); got != 4 {
		t.Errorf("ExitCode() = %d, want 4", got)
	}
	if !errors.Is(err, inner) {
		t.Errorf("expected errors.Is to see through Unwrap to the inner error")
	}
}

func TestUsage(t *testing.T) {
	if got := Usage(nil); got != nil {
		t.Errorf("Usage(nil) = %v, want nil", got)
	}

	inner := errors.New("missing arg")
	got := Usage(inner)

	var ec ExitCoder
	if !errors.As(got, &ec) {
		t.Fatalf("Usage(err) does not satisfy ExitCoder")
	}
	if ec.ExitCode() != 2 {
		t.Errorf("Usage(err).ExitCode() = %d, want 2", ec.ExitCode())
	}
	if got.Error() != inner.Error() {
		t.Errorf("Usage(err).Error() = %q, want %q", got.Error(), inner.Error())
	}
}

func TestWrapArgs(t *testing.T) {
	t.Run("passes through success", func(t *testing.T) {
		fn := WrapArgs(cobra.ExactArgs(1))
		if err := fn(&cobra.Command{}, []string{"demo"}); err != nil {
			t.Errorf("expected no error, got %v", err)
		}
	})

	t.Run("wraps validation failure as a usage error", func(t *testing.T) {
		fn := WrapArgs(cobra.ExactArgs(1))
		err := fn(&cobra.Command{}, nil)
		if err == nil {
			t.Fatalf("expected an error")
		}

		var ec ExitCoder
		if !errors.As(err, &ec) {
			t.Fatalf("expected error to satisfy ExitCoder, got %v", err)
		}
		if ec.ExitCode() != 2 {
			t.Errorf("ExitCode() = %d, want 2", ec.ExitCode())
		}
	})
}
