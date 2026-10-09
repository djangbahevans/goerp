package module

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestBuildWorkerStagesNativeBinaryByChecksum(t *testing.T) {
	dir := t.TempDir()
	storage := t.TempDir()
	for path, data := range map[string]string{
		"manifest.json":    `{"workflow_types":[{"name":"example"}]}`,
		"go.mod":           "module example.test/worker\n\ngo 1.27.0\n",
		"workflow/main.go": "package main\nfunc main() {}\n",
	} {
		full := filepath.Join(dir, path)
		if err := os.MkdirAll(filepath.Dir(full), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	if err := BuildWorker(t.Context(), dir, storage); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "build", "workflow-worker"))
	if err != nil {
		t.Fatal(err)
	}
	staged, err := os.ReadFile(filepath.Join(storage, "sha256:"+computeSHA256(data)))
	if err != nil || !bytes.Equal(data, staged) {
		t.Fatalf("staged worker does not match its checksum: %v", err)
	}

	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(dir, "workflow")); err != nil {
		t.Fatal(err)
	}
	if err := BuildWorker(t.Context(), dir, storage); err != nil {
		t.Fatalf("module without workflows tried to compile a worker: %v", err)
	}
}
