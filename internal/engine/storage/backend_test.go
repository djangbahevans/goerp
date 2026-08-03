package storage

import (
	"errors"
	"testing"
)

func TestNew(t *testing.T) {
	t.Setenv("GOERP_STORAGE_LOCAL_DIR", t.TempDir())

	cases := []struct {
		backendType string
		wantErr     error
		wantType    bool
	}{
		{"local", nil, true},
		{"seaweedfs", ErrSeaweedFsNotSupported, false},
		{"s3", ErrS3NotSupported, false},
		{"r2", ErrR2NotSupported, false},
		{"gcs", ErrGCSNotSupported, false},
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
