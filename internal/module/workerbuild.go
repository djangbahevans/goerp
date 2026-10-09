package module

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
)

// BuildWorker compiles workflow/ when workflow_types is declared. The native binary
// remains separate from the package; storageDir optionally stages a checksum-keyed copy.
func BuildWorker(ctx context.Context, dir, storageDir string) error {
	decoded, err := readManifestJSON(filepath.Join(dir, "manifest.json"))
	if err != nil {
		return err
	}

	workflows, _ := decoded["workflow_types"].([]any)
	if len(workflows) == 0 {
		return nil
	}

	if err := os.MkdirAll(filepath.Join(dir, "build"), 0o755); err != nil {
		return fmt.Errorf("create worker build directory: %w", err)
	}

	if err := runCmd(ctx, dir, []string{"CGO_ENABLED=0"}, "go", "build", "-o", "build/workflow-worker", "./workflow"); err != nil {
		return err
	}
	if storageDir == "" {
		return nil
	}

	data, err := os.ReadFile(filepath.Join(dir, "build", "workflow-worker"))
	if err != nil {
		return fmt.Errorf("read workflow-worker: %w", err)
	}
	if err := os.MkdirAll(storageDir, 0o700); err != nil {
		return fmt.Errorf("create worker storage directory: %w", err)
	}

	return os.WriteFile(filepath.Join(storageDir, "sha256:"+computeSHA256(data)), data, 0o600)
}
