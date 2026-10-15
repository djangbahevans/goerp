package storage

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/djangbahevans/goerp/internal/engine/config"
)

func TestNew(t *testing.T) {
	backend, err := New(t.Context(), config.Config{
		StorageBackend:  "local",
		StorageLocalDir: t.TempDir(),
	})
	if err != nil || backend == nil {
		t.Fatalf("local backend = %v, %v", backend, err)
	}

	backend, err = New(t.Context(), config.Config{
		StorageBackend: "nonsense",
	})
	if !errors.Is(err, ErrUnknownBackend) || backend != nil {
		t.Fatalf("unknown backend = %v, %v", backend, err)
	}
}

func TestNewLocalFailureReturnsNilBackend(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(dir, nil, 0o600); err != nil {
		t.Fatal(err)
	}

	backend, err := New(t.Context(), config.Config{
		StorageBackend:  "local",
		StorageLocalDir: dir,
	})
	if err == nil || backend != nil {
		t.Fatalf("failed local construction = %v, %v", backend, err)
	}
}
