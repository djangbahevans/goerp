package workflowworker

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
	"uuid"

	"github.com/djangbahevans/goerp/internal/engine/manifest"
	"github.com/djangbahevans/goerp/internal/engine/module"
	"github.com/djangbahevans/goerp/internal/engine/storage"
)

func TestRespawnSameChecksumPreservesExecutableAndRotatesCredential(t *testing.T) {
	client := newTestTemporalClient(t)
	defer client.Close()
	backend := newLocalStorage(t)
	data := buildTestWorkerVariant(t, "same-checksum")
	checksum := fmt.Sprintf("sha256:%x", sha256.Sum256(data))
	if _, err := backend.Upload(t.Context(), checksum, bytes.NewReader(data), storage.UploadOptions{}); err != nil {
		t.Fatal(err)
	}
	mod := &module.LoadedModule{
		Manifest: manifest.Manifest{
			Name:           "dev_" + uuid.NewV7().String(),
			WorkerChecksum: checksum,
			WorkflowTypes:  []manifest.WorkflowType{{Name: "example"}},
		},
	}
	cacheDir := t.TempDir()
	m := NewManager(backend, client, cacheDir)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		m.StopAll(ctx)
	})
	if err := m.spawn(t.Context(), mod); err != nil {
		t.Fatal(err)
	}
	m.mu.Lock()
	old := m.processes[mod.Manifest.Name]
	m.mu.Unlock()
	if err := m.Respawn(t.Context(), mod); err != nil {
		t.Fatal(err)
	}
	if m.Validate(old.credential.Token, mod.Manifest.Name) {
		t.Fatal("same-checksum reload retained the old credential")
	}
	path := filepath.Join(cacheDir, mod.Manifest.Name, checksumDirName(checksum), "workflow-worker")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("replacement worker executable was removed: %v", err)
	}
	select {
	case <-old.done:
	case <-time.After(10 * time.Second):
		t.Fatal("old worker survived replacement")
	}
}
