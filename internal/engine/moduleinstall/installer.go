// Package moduleinstall installs new modules by persisting and loading packages, syncing
// tenant schemas and publishing the live registry. Upgrades use the hot-reload path;
// install does not verify package signatures.
package moduleinstall

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/djangbahevans/goerp/internal/engine/jobqueue"
	"github.com/djangbahevans/goerp/internal/engine/moduleboot"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"
)

// ErrInvalidPackage wraps a StartInstall failure caused by data itself
// being malformed (not a well-formed .erp package, or an unparseable
// manifest) — a genuine client input error, distinct from everything
// after that point (persisting to disk, enqueuing the job), which are
// infra failures. adminapi's handler uses errors.Is against this to
// choose 400 vs 500, the same dispatch shape schema.go's
// writeSchemaResolveError uses for tenant.ErrTenantNotFound/
// tenantsync.ErrModuleNotLoaded.
var ErrInvalidPackage = errors.New("invalid module package")

// Installer satisfies adminapi.ModuleInstaller — the POST
// /admin/modules/install handler's entry point into this package.
type Installer struct {
	// ModuleDir persists packages in the startup discovery directory so installed modules
	// survive engine restarts.
	ModuleDir string
	JobClient *river.Client[pgx.Tx]
	JobQueue  string
}

// StartInstall validates archive structure, persists it under the declared name/version
// and enqueues asynchronous loading. Atomic rename prevents readers from seeing partial
// packages.
func (i *Installer) StartInstall(ctx context.Context, data []byte) (jobID string, err error) {
	_, mf, err := moduleboot.ParsePackage(data)
	if err != nil {
		return "", fmt.Errorf("%w: %w", ErrInvalidPackage, err)
	}

	path := filepath.Join(i.ModuleDir, fmt.Sprintf("%s-%s.erp", mf.Name, mf.Version))
	if err := writeFileAtomic(path, data); err != nil {
		return "", fmt.Errorf("persist package: %w", err)
	}

	insertResult, err := i.JobClient.Insert(ctx, Args{PackagePath: path}, &river.InsertOpts{Queue: i.JobQueue})
	if err != nil {
		return "", fmt.Errorf("enqueue install job: %w", err)
	}
	return jobqueue.EncodeJobID(insertResult.Job.ID), nil
}

// A same-directory temporary file makes the rename atomic for readers; random names
// isolate concurrent installs.
func writeFileAtomic(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create module dir: %w", err)
	}

	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	tmpPath := tmp.Name()
	defer func() { _ = os.Remove(tmpPath) }()

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp file: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("rename into place: %w", err)
	}
	return nil
}
