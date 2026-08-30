// Package moduleinstall implements goerp#468's engine-side orchestration
// for `goerp module install`, scoped to installing a module the engine
// has never loaded before: persist the submitted .erp package, compile it
// and fetch its declarations (loader.LoadModule, the same Stage 3 path
// engine startup uses), sync every active tenant's schema
// (tenantsync.SyncModule), publish it into the live registry, and — if it
// declares workflow_types — spawn its workflow-worker.
//
// Installing a new version of an already-loaded module (the binary-swap/
// downgrade-check/route-merge upgrade path) is out of scope here — that's
// hot reload's leader path (goerp#467). So is a `registry_ref` install
// and signature-chain verification, both blocked on the module registry
// artifact pipeline (backlog #563, not yet built): Worker runs with no
// signature gate, equivalent to always operating under the documented
// GOERP_ENV=development bypass (security-model.md §5) — there is
// currently no production safety gate on module install.
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
	// ModuleDir is the same directory moduleboot.Discover scans at
	// startup — persisting the submitted package here means a later
	// engine restart picks the installed module back up the same way it
	// does any other, without this package having to duplicate Discover's
	// own bookkeeping.
	ModuleDir string
	JobClient *river.Client[pgx.Tx]
	JobQueue  string
}

// StartInstall validates data as a well-formed .erp package (a fast,
// synchronous check — the actual compile and cross-tenant schema sync are
// what's genuinely async, not the package's own structural validity),
// persists it under ModuleDir, and enqueues the install job. The package
// is written under its own declared name/version
// ("{name}-{version}.erp", goerp module build's own output naming
// convention) via a temp-file-then-rename so a concurrent reader (a
// restart's Discover, another install of a different module) never
// observes a partially-written file; re-installing the same
// name/version overwrites it, which is fine — SyncModule's own
// already-synced short-circuit and Diff's live-inspection-based diff
// both make a repeat install of identical content a no-op past that
// point.
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

// writeFileAtomic writes data to path via a temp file in the same
// directory, then renames it into place — a reader (another process, or
// this same one on a later Discover) never observes a partial write, and
// os.CreateTemp's own randomized suffix means two concurrent installs
// (of different packages) never collide on the temp name.
func writeFileAtomic(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create module dir: %w", err)
	}

	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	tmpPath := tmp.Name()
	defer func() { _ = os.Remove(tmpPath) }() // no-op once the rename below succeeds

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
