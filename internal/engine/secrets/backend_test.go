package secrets

import (
	"context"
	"errors"
	"testing"
)

func TestNew(t *testing.T) {
	cases := []struct {
		backendType string
		wantErr     error
		wantType    bool // true if a non-nil Backend should be returned
	}{
		{"env", nil, true},
		{"vault", ErrVaultNotSupported, false},
		{"aws_secretsmanager", ErrAwsNotSupported, false},
		{"nonsense", ErrUnknownBackend, false},
	}

	for _, c := range cases {
		t.Run(c.backendType, func(t *testing.T) {
			backend, err := New(c.backendType)

			if !errors.Is(err, c.wantErr) {
				t.Errorf("New(%q) error = %v, want %v", c.backendType, err, c.wantErr)
			}
			if c.wantType && backend == nil {
				t.Errorf("New(%q) returned a nil backend, want non-nil", c.backendType)
			}
			if !c.wantType && backend != nil {
				t.Errorf("New(%q) returned a non-nil backend, want nil", c.backendType)
			}
		})
	}
}

func TestEnvBackendGet(t *testing.T) {
	t.Setenv("MY_TEST_SECRET", "hunter2")

	b := &EnvBackend{}
	got, err := b.Get(context.Background(), "MY_TEST_SECRET")
	if err != nil {
		t.Fatalf("Get() error: %v", err)
	}
	if got != "hunter2" {
		t.Errorf("Get() = %q, want %q", got, "hunter2")
	}
}

func TestEnvBackendGetUnset(t *testing.T) {
	b := &EnvBackend{}
	got, err := b.Get(context.Background(), "THIS_ENV_VAR_DOES_NOT_EXIST")
	if err != nil {
		t.Fatalf("Get() error: %v", err)
	}
	if got != "" {
		t.Errorf("Get() = %q, want empty string for an unset var", got)
	}
}

func TestEnvBackendSet(t *testing.T) {
	b := &EnvBackend{}
	if err := b.Set(context.Background(), "key", "value"); !errors.Is(err, ErrSetNotSupported) {
		t.Errorf("Set() error = %v, want %v", err, ErrSetNotSupported)
	}
}

func TestEnvBackendRotate(t *testing.T) {
	b := &EnvBackend{}
	if _, err := b.Rotate(context.Background(), "key"); !errors.Is(err, ErrRotateNotSupported) {
		t.Errorf("Rotate() error = %v, want %v", err, ErrRotateNotSupported)
	}
}
