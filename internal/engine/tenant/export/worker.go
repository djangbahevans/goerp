package tenantexport

import (
	"bufio"
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"slices"
	"strconv"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/auth/rowcrypt"
	"github.com/djangbahevans/goerp/internal/engine/checkpoint"
	"github.com/djangbahevans/goerp/internal/engine/modeltable"
	"github.com/djangbahevans/goerp/internal/engine/module"
	"github.com/djangbahevans/goerp/internal/engine/registry"
	"github.com/djangbahevans/goerp/internal/engine/storage"
	"github.com/djangbahevans/goerp/internal/engine/tenant"
	"github.com/djangbahevans/goerp/internal/engine/tenant/transfer"
	"github.com/riverqueue/river"
)

// Bulk table dumps have no per-batch heartbeat, so leases allow long uninterrupted reads.
const defaultLeaseStaleAfter = 30 * time.Minute

const signedURLExpiry = time.Hour

// Worker assembles an encrypted tenant archive with module and engine-table checkpoints.
type Worker struct {
	river.WorkerDefaults[Args]

	TenantStore *tenant.Store
	Registry    *registry.ModuleRegistry
	// Bulk reads require the schema-sync pool to bypass tenant RLS.
	RawDB          *sql.DB
	Checkpoints    *checkpoint.Store
	StorageBackend storage.Backend
	// Archive keys are encrypted before River persists the result.
	Keys            *rowcrypt.RowKeySet
	LeaseStaleAfter time.Duration
}

func (w *Worker) Work(ctx context.Context, job *river.Job[Args]) error {
	result, err := w.run(ctx, job)
	if err != nil {
		return err
	}
	if err := river.RecordOutput(ctx, result); err != nil {
		return fmt.Errorf("record job output: %w", err)
	}
	return nil
}

func (w *Worker) run(ctx context.Context, job *river.Job[Args]) (Result, error) {
	a := job.Args

	t, err := w.TenantStore.GetBySlug(ctx, a.TenantSlug)
	if err != nil {
		return Result{}, fmt.Errorf("look up tenant %q: %w", a.TenantSlug, err)
	}
	if t.Status == tenant.StatusDeleted {
		return Result{}, fmt.Errorf("tenant %q is deleted, nothing to export", a.TenantSlug)
	}

	snap := w.Registry.Snapshot()
	if snap == nil {
		return Result{}, fmt.Errorf("module registry not ready")
	}

	modules := inScopeModules(snap.Modules(), a.Include, a.Exclude)

	jobIDStr := strconv.FormatInt(job.ID, 10)
	leaseOwner := fmt.Sprintf("%d-%d", job.ID, job.Attempt)
	staleAfter := w.LeaseStaleAfter
	if staleAfter <= 0 {
		staleAfter = defaultLeaseStaleAfter
	}
	basePrefix := fmt.Sprintf("exports/%s/%s", t.ID, jobIDStr)
	modulesPrefix := basePrefix + "/modules/"

	man := manifest{TenantID: t.ID, TenantSlug: t.Slug, ExportedAt: time.Now().UTC()}
	for _, name := range modules {
		man.Modules = append(man.Modules, manifestModule{
			Name:    name,
			Version: snap.Modules()[name].Manifest.Version,
			File:    name + ".jsonl",
		})
	}

	for _, name := range modules {
		progress, err := w.Checkpoints.AcquireLease(ctx, jobIDStr, name, t.ID, leaseOwner, staleAfter)
		if err != nil {
			if errors.Is(err, checkpoint.ErrAlreadyComplete) {
				continue // a previous attempt already dumped and uploaded this module
			}
			return Result{}, fmt.Errorf("acquire lease for module %q: %w", name, err)
		}

		mod := *snap.Modules()[name]
		mod.ModelDecls = snap.Models(name)
		data, err := w.dumpModule(ctx, t.Slug, &mod)
		if err != nil {
			_ = progress.MarkFailed(ctx)
			return Result{}, fmt.Errorf("dump module %q: %w", name, err)
		}

		key := modulesPrefix + name + ".jsonl"
		if _, err := w.StorageBackend.Upload(ctx, key, bytes.NewReader(data), storage.UploadOptions{ContentType: "application/x-ndjson"}); err != nil {
			_ = progress.MarkFailed(ctx)
			return Result{}, fmt.Errorf("upload module %q data: %w", name, err)
		}
		if err := progress.MarkComplete(ctx); err != nil {
			return Result{}, fmt.Errorf("mark module %q complete: %w", name, err)
		}
	}

	moduleData := make(map[string][]byte, len(modules))
	for _, name := range modules {
		rc, _, err := w.StorageBackend.Download(ctx, modulesPrefix+name+".jsonl")
		if err != nil {
			return Result{}, fmt.Errorf("download module %q data for assembly: %w", name, err)
		}
		data, err := io.ReadAll(rc)
		_ = rc.Close()
		if err != nil {
			return Result{}, fmt.Errorf("read module %q data for assembly: %w", name, err)
		}
		moduleData[name] = data
	}

	if err := w.exportEngineData(ctx, job, t, &man, moduleData, snap, basePrefix, staleAfter); err != nil {
		return Result{}, err
	}

	plaintext, err := buildArchive(man, moduleData)
	if err != nil {
		return Result{}, fmt.Errorf("build archive: %w", err)
	}
	ciphertext, decryptionKey, err := encryptArchive(plaintext)
	if err != nil {
		return Result{}, fmt.Errorf("encrypt archive: %w", err)
	}
	checksum := checksumHex(ciphertext)

	archiveKey := basePrefix + "/archive.zip.enc"
	if _, err := w.StorageBackend.Upload(ctx, archiveKey, bytes.NewReader(ciphertext), storage.UploadOptions{ContentType: "application/octet-stream"}); err != nil {
		return Result{}, fmt.Errorf("upload archive: %w", err)
	}
	downloadURL, err := w.StorageBackend.SignedURL(ctx, archiveKey, signedURLExpiry)
	if err != nil {
		return Result{}, fmt.Errorf("generate signed download URL: %w", err)
	}

	// Staging objects remain available for retries under the export retention window.
	encryptedKey, err := w.Keys.Encrypt([]byte(decryptionKey))
	if err != nil {
		return Result{}, fmt.Errorf("encrypt decryption key for storage: %w", err)
	}

	return Result{
		DownloadURL:   downloadURL,
		Checksum:      checksum,
		DecryptionKey: string(encryptedKey),
	}, nil
}

func inScopeModules(modules map[string]*module.LoadedModule, include, exclude []string) []string {
	includeSet := toSet(include)
	excludeSet := toSet(exclude)

	names := make([]string, 0, len(modules))
	for name, m := range modules {
		if m.Status != module.StatusReady {
			continue
		}
		if len(includeSet) > 0 && !includeSet[name] {
			continue
		}
		if excludeSet[name] {
			continue
		}
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}

func toSet(names []string) map[string]bool {
	set := make(map[string]bool, len(names))
	for _, n := range names {
		set[n] = true
	}
	return set
}

func (w *Worker) dumpModule(ctx context.Context, tenantSlug string, mod *module.LoadedModule) ([]byte, error) {
	var buf bytes.Buffer
	bw := bufio.NewWriter(&buf)

	tx, err := beginTenantScopedRead(ctx, w.RawDB, tenantSlug)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	for _, md := range mod.ModelDecls {
		if md.Backend != "" {
			continue
		}
		pkCol, ok := primaryKeyColumn(md)
		if !ok {
			continue
		}
		cols := exportableColumns(md)
		if len(cols) == 0 {
			continue
		}

		data, err := transfer.Dump(ctx, tx, modeltable.Name(md), md.Name, cols, "", " ORDER BY "+quoteIdent(pkCol))
		if err != nil {
			return nil, fmt.Errorf("dump model %s: %w", md.Name, err)
		}
		if _, err := bw.Write(data); err != nil {
			return nil, err
		}
	}

	if err := bw.Flush(); err != nil {
		return nil, fmt.Errorf("flush module dump: %w", err)
	}
	return buf.Bytes(), nil
}

type exportRecord struct {
	Model  string         `json:"model"`
	Record map[string]any `json:"record"`
}

func beginTenantScopedRead(ctx context.Context, db *sql.DB, tenantSlug string) (*sql.Tx, error) {
	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, fmt.Errorf("begin transaction: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `SELECT set_config('search_path', $1, true)`, "tenant_"+tenantSlug+", public"); err != nil {
		_ = tx.Rollback()
		return nil, fmt.Errorf("set search_path: %w", err)
	}
	return tx, nil
}
