package tenantexport

import (
	"bufio"
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/auth/rowcrypt"
	"github.com/djangbahevans/goerp/internal/engine/checkpoint"
	"github.com/djangbahevans/goerp/internal/engine/module"
	"github.com/djangbahevans/goerp/internal/engine/registry"
	"github.com/djangbahevans/goerp/internal/engine/storage"
	"github.com/djangbahevans/goerp/internal/engine/tenant"
	"github.com/riverqueue/river"
)

// defaultLeaseStaleAfter is how long a module's lease_heartbeat may go
// without renewal before a retry attempt reclaims it as presumed-dead —
// generous, since a module's full data dump is one uninterrupted
// operation with no per-batch heartbeat renewal in this implementation.
const defaultLeaseStaleAfter = 30 * time.Minute

// signedURLExpiry matches cli-reference.md §5's documented default.
const signedURLExpiry = time.Hour

// Worker runs Args by walking every in-scope module, dumping its
// exportable columns to a per-module object in a job-scoped storage
// prefix (checkpointed complete once uploaded), then — once every module
// is complete — assembling those objects into the final encrypted
// archive. Re-invoked from scratch by River on retry (Work is written to
// tolerate that, same convention internal/engine/tenant/offboard's
// ImmediateWorker documents): AcquireLease skips a module already marked
// complete in a previous attempt rather than re-querying and
// re-uploading it.
type Worker struct {
	river.WorkerDefaults[Args]

	TenantStore *tenant.Store
	Registry    *registry.ModuleRegistry
	// RawDB is schema_sync_user's connection pool (schema.SchemaSyncPool.Raw),
	// which has BYPASSRLS — export needs unfiltered access to every row
	// in the tenant's schema, not the RLS-constrained view host.orm's
	// primary pool enforces for a live caller.
	RawDB          *sql.DB
	Checkpoints    *checkpoint.Store
	StorageBackend storage.Backend
	// Keys encrypts Result.DecryptionKey before it's recorded via
	// river.RecordOutput (goerp#453) — river_job persists Output as-is for
	// the life of the job row, so the archive's one-time decryption key
	// doesn't sit there in plaintext. adminapi/jobs.go's OutputDecryptor
	// hook (wired to DecryptOutput in this package) decrypts it back
	// transparently for a legitimate, already-admin-authenticated poller.
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

// run is Work's plain-Go core, callable without a real River execution
// context in the loop (river.RecordOutput panics/errors outside one) —
// the shared entry point for both Work and this package's own tests,
// mirroring internal/engine/wasm/host_orm.go's ORMSearch/ORMSearchRead
// split between WASM plumbing and testable core logic.
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
	if len(modules) == 0 {
		return Result{}, fmt.Errorf("no modules in scope for export")
	}

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

		data, err := w.dumpModule(ctx, t.Slug, snap.Modules()[name])
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

	// Per-module staging objects are deliberately not deleted here: a
	// re-run of this same job (whether a genuine retry, or the job simply
	// invoked again after already completing) must still be able to
	// re-assemble the archive from them without re-querying the database.
	// They share the same object-storage retention window as the final
	// archive (cli-reference.md §5's 7-day auto-delete) rather than being
	// cleaned up by the worker itself.

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

// inScopeModules returns every StatusReady module name, filtered by
// include (if non-empty, only these) then exclude, sorted for a
// deterministic, resumable processing order.
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
	sort.Strings(names)
	return names
}

func toSet(names []string) map[string]bool {
	set := make(map[string]bool, len(names))
	for _, n := range names {
		set[n] = true
	}
	return set
}

// dumpModule writes every eligible Table-backed model's rows in mod, one
// JSON object per line prefixed with which model it belongs to, ordered
// by each model's primary key for a stable, resumable-in-spirit scan
// order. A model with no declared primary key, or a non-Table backend
// (Virtual/Transient — no tenant-schema table to dump), is skipped.
func (w *Worker) dumpModule(ctx context.Context, tenantSlug string, mod *module.LoadedModule) ([]byte, error) {
	var buf bytes.Buffer
	bw := bufio.NewWriter(&buf)
	enc := json.NewEncoder(bw)

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

		quotedCols := make([]string, len(cols))
		for i, c := range cols {
			quotedCols[i] = quoteIdent(c)
		}
		sqlStr := fmt.Sprintf("SELECT %s FROM %s ORDER BY %s ASC",
			strings.Join(quotedCols, ", "), quoteIdent(tableNameFor(md)), quoteIdent(pkCol))

		rows, err := tx.QueryContext(ctx, sqlStr)
		if err != nil {
			return nil, fmt.Errorf("query %s: %w", md.Name, err)
		}
		if err := writeRowsAsJSONLines(rows, md.Name, cols, enc); err != nil {
			rows.Close()
			return nil, fmt.Errorf("scan %s: %w", md.Name, err)
		}
		rows.Close()
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

func writeRowsAsJSONLines(rows *sql.Rows, modelName string, cols []string, enc *json.Encoder) error {
	values := make([]any, len(cols))
	ptrs := make([]any, len(cols))
	for i := range values {
		ptrs[i] = &values[i]
	}

	for rows.Next() {
		if err := rows.Scan(ptrs...); err != nil {
			return err
		}
		record := make(map[string]any, len(cols))
		for i, col := range cols {
			record[col] = values[i]
		}
		if err := enc.Encode(exportRecord{Model: modelName, Record: record}); err != nil {
			return err
		}
	}
	return rows.Err()
}

// beginTenantScopedRead opens a read-only transaction against
// tenantSlug's schema through w.RawDB (schema_sync_user, BYPASSRLS) — no
// app.current_user_* session variables to set, unlike
// internal/engine/wasm's own beginTenantScopedRead, since there is no
// live caller for RLS policies to evaluate against and this connection
// bypasses them entirely.
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
