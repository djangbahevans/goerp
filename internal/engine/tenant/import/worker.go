package tenantimport

import (
	"bufio"
	"bytes"
	"context"
	"database/sql"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"
	"uuid"

	"github.com/Masterminds/semver/v3"
	abiv1 "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/djangbahevans/goerp/internal/engine/auth/rowcrypt"
	"github.com/djangbahevans/goerp/internal/engine/checkpoint"
	"github.com/djangbahevans/goerp/internal/engine/httperr"
	"github.com/djangbahevans/goerp/internal/engine/modeltable"
	"github.com/djangbahevans/goerp/internal/engine/module"
	"github.com/djangbahevans/goerp/internal/engine/registry"
	"github.com/djangbahevans/goerp/internal/engine/role"
	"github.com/djangbahevans/goerp/internal/engine/storage"
	"github.com/djangbahevans/goerp/internal/engine/tenant"
	tenantprovision "github.com/djangbahevans/goerp/internal/engine/tenant/provision"
	"github.com/djangbahevans/goerp/internal/engine/tenant/transfer"
	"github.com/djangbahevans/goerp/sdk/go/model"
	"github.com/riverqueue/river"
)

const defaultLeaseStaleAfter = 30 * time.Minute

// Worker restores a tenant archive with checkpoints for data, files and invitations.
type Worker struct {
	river.WorkerDefaults[Args]

	TenantStore *tenant.Store
	Registry    *registry.ModuleRegistry
	// Bulk writes require the schema-sync pool to bypass tenant RLS.
	RawDB           *sql.DB
	Checkpoints     *checkpoint.Store
	StorageBackend  storage.Backend
	Provision       *tenantprovision.Activities
	Keys            *rowcrypt.RowKeySet
	LeaseStaleAfter time.Duration
	Invites         Inviter
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

	rc, _, err := w.StorageBackend.Download(ctx, a.InputRef)
	if err != nil {
		return Result{}, fmt.Errorf("download archive %q: %w", a.InputRef, err)
	}
	ciphertext, err := io.ReadAll(rc)
	_ = rc.Close()
	if err != nil {
		return Result{}, fmt.Errorf("read archive %q: %w", a.InputRef, err)
	}

	decryptionKey, err := w.Keys.Decrypt([]byte(a.DecryptionKey))
	if err != nil {
		return Result{}, fmt.Errorf("decrypt stored decryption key: %w", err)
	}

	plaintext, err := decryptArchive(ciphertext, string(decryptionKey))
	if err != nil {
		return Result{}, err
	}

	man, moduleData, err := parseArchive(plaintext)
	if err != nil {
		return Result{}, err
	}

	if err := validateArchive(man, moduleData); err != nil {
		return Result{}, err
	}

	snap := w.Registry.Snapshot()
	if snap == nil {
		return Result{}, fmt.Errorf("module registry not ready")
	}
	if err := checkModuleVersions(man, snap.Modules()); err != nil {
		return Result{}, err
	}

	t, err := w.resolveTenant(ctx, job)
	if err != nil {
		return Result{}, err
	}

	result, err := w.provisionAndLoad(ctx, job, t, man, moduleData, snap)
	if err != nil {
		if job.Attempt >= job.MaxAttempts {
			if relErr := w.abandonImport(ctx, t); relErr != nil && !errors.Is(relErr, tenant.ErrTenantNotFound) {
				return Result{}, fmt.Errorf("%w (also failed to release slug %q after final attempt: %w)", err, a.NewSlug, relErr)
			}
		}
		return Result{}, err
	}
	return result, nil
}

func (w *Worker) provisionAndLoad(ctx context.Context, job *river.Job[Args], t *tenant.Tenant, man manifest, moduleData map[string][]byte, snap *registry.RegistrySnapshot) (Result, error) {
	moduleNames := make([]string, len(man.Modules))
	for i, m := range man.Modules {
		moduleNames[i] = m.Name
	}
	if err := w.checkpointStep(ctx, job, t, "engine:bootstrap", func() error {
		if err := w.Provision.CreateTenantSchema(ctx, t.Slug); err != nil {
			return err
		}
		if err := w.Provision.CreateEngineTables(ctx, t.Slug); err != nil {
			return err
		}
		for _, m := range man.Modules {
			if err := w.Provision.SyncModuleSchema(ctx, t.ID, t.Slug, m.Name, abiv1.Request{ID: httperr.RequestIDFromContext(ctx), TraceID: httperr.TraceIDFromContext(ctx)}); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		return Result{}, fmt.Errorf("bootstrap import: %w", err)
	}

	mapping, err := w.mapAccounts(ctx, job, t, moduleData["accounts.json"])
	if err != nil {
		return Result{}, err
	}
	for _, table := range transfer.Tables() {
		if err := w.checkpointStep(ctx, job, t, "engine:"+table.Name, func() error {
			return w.loadEngineTable(ctx, t, table, moduleData[table.File], mapping)
		}); err != nil {
			return Result{}, fmt.Errorf("load engine table %s: %w", table.Name, err)
		}
	}

	jobIDStr := strconv.FormatInt(job.ID, 10)
	leaseOwner := fmt.Sprintf("%d-%d", job.ID, job.Attempt)
	staleAfter := w.LeaseStaleAfter
	if staleAfter <= 0 {
		staleAfter = defaultLeaseStaleAfter
	}

	for _, m := range man.Modules {
		progress, err := w.Checkpoints.AcquireLease(ctx, jobIDStr, m.Name, t.ID, leaseOwner, staleAfter)
		if err != nil {
			if errors.Is(err, checkpoint.ErrAlreadyComplete) {
				continue
			}
			return Result{}, fmt.Errorf("acquire lease for module %q: %w", m.Name, err)
		}

		mod, ok := snap.Modules()[m.Name]
		if !ok {
			_ = progress.MarkFailed(ctx)
			return Result{}, fmt.Errorf("module %q is no longer loaded", m.Name)
		}

		resolved := *mod
		resolved.ModelDecls = snap.Models(m.Name)
		if err := w.loadModule(ctx, t.Slug, &resolved, moduleData[m.Name], loadIdentities{Mapping: mapping.IDs, TenantID: t.ID}); err != nil {
			_ = progress.MarkFailed(ctx)
			return Result{}, fmt.Errorf("load module %q: %w", m.Name, err)
		}
		if err := progress.MarkComplete(ctx); err != nil {
			return Result{}, fmt.Errorf("mark module %q complete: %w", m.Name, err)
		}
	}

	if err := role.NewStore(w.RawDB).ReindexMemberships(ctx, t.ID, t.Slug); err != nil {
		return Result{}, err
	}

	if err := w.restoreFiles(ctx, job, t, moduleData); err != nil {
		return Result{}, err
	}
	if err := w.Provision.ActivateTenant(ctx, t.Slug); err != nil {
		return Result{}, err
	}
	if err := w.inviteRestoredMembers(ctx, job, t); err != nil {
		return Result{}, err
	}

	return Result{TenantID: t.ID, TenantSlug: t.Slug, ModulesImported: moduleNames, AccountsMatched: mapping.Matched, AccountsCreated: mapping.Created}, nil
}

// A persisted tenant ID binds retries to this job, including retries after activation.
func (w *Worker) resolveTenant(ctx context.Context, job *river.Job[Args]) (*tenant.Tenant, error) {
	slug := job.Args.NewSlug
	jobID := strconv.FormatInt(job.ID, 10)
	var tenantID string
	if err := w.RawDB.QueryRowContext(ctx, `
  INSERT INTO system.job_checkpoints (job_id, module, tenant_id, status)
  VALUES ($1, 'engine:tenant', $2, 'complete')
  ON CONFLICT (job_id, module) DO UPDATE SET tenant_id = system.job_checkpoints.tenant_id
  RETURNING tenant_id`, jobID, uuid.NewV7().String()).Scan(&tenantID); err != nil {
		return nil, fmt.Errorf("reserve import identity: %w", err)
	}

	if _, err := w.TenantStore.ReserveSlug(ctx, tenantID, slug, slug); err != nil {
		return nil, fmt.Errorf("reserve import slug: %w", err)
	}
	t, err := w.TenantStore.GetBySlug(ctx, slug)
	if err != nil {
		return nil, err
	}
	if t.ID != tenantID || (t.Status != tenant.StatusProvisioning && t.Status != tenant.StatusActive) {
		return nil, fmt.Errorf("tenant %q is unavailable for import", slug)
	}
	return t, nil
}

func checkModuleVersions(man manifest, loaded map[string]*module.LoadedModule) error {
	var problems []string
	for _, m := range man.Modules {
		mod, ok := loaded[m.Name]
		if !ok {
			problems = append(problems, fmt.Sprintf("%s@%s: not installed", m.Name, m.Version))
			continue
		}

		archived, err1 := semver.NewVersion(m.Version)
		installed, err2 := semver.NewVersion(mod.Manifest.Version)
		if err1 != nil || err2 != nil || !archived.Equal(installed) {
			problems = append(problems, fmt.Sprintf("%s@%s: installed version is %s", m.Name, m.Version, mod.Manifest.Version))
		}
	}
	if len(problems) > 0 {
		return fmt.Errorf("module version mismatch (run `goerp module install <name>@<version>` for each): %s", strings.Join(problems, "; "))
	}
	return nil
}

func (w *Worker) loadModule(ctx context.Context, tenantSlug string, mod *module.LoadedModule, data []byte, identities ...loadIdentities) error {
	if len(data) == 0 {
		return nil
	}

	declByName := make(map[string]model.ModelDeclaration, len(mod.ModelDecls))
	for _, md := range mod.ModelDecls {
		declByName[md.Name] = md
	}

	tx, err := beginTenantScopedWrite(ctx, w.RawDB, tenantSlug)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	stmts := make(map[string]*sql.Stmt)
	columns := make(map[string][]transfer.Column)
	defer func() {
		for _, stmt := range stmts {
			_ = stmt.Close()
		}
	}()

	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var rec exportRecord
		if err := json.Unmarshal(line, &rec, json.WithUnmarshalers(numberPreservingUnmarshalers)); err != nil {
			return fmt.Errorf("decode record: %w", err)
		}

		md, ok := declByName[rec.Model]
		if !ok {
			return fmt.Errorf("archive references unknown model %q", rec.Model)
		}

		stmt, ok := stmts[rec.Model]
		if !ok {
			stmt, err = prepareInsert(ctx, tx, md)
			if err != nil {
				return fmt.Errorf("prepare insert for model %q: %w", rec.Model, err)
			}
			stmts[rec.Model] = stmt
			if len(identities) > 0 {
				cols, err := transfer.Columns(ctx, tx, "tenant_"+tenantSlug, modeltable.Name(md))
				if err != nil {
					return err
				}
				columns[rec.Model] = cols
			}
		}

		if len(identities) > 0 {
			if err := rewriteIdentities(rec.Record, columns[rec.Model], identities[0].Mapping, identities[0].TenantID); err != nil {
				return err
			}
		}

		args, err := insertArgs(md, rec.Record)
		if err != nil {
			return fmt.Errorf("build insert args for model %q: %w", rec.Model, err)
		}
		if _, err := stmt.ExecContext(ctx, args...); err != nil {
			return fmt.Errorf("insert %s row: %w", rec.Model, err)
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("scan module data: %w", err)
	}

	return tx.Commit()
}

func prepareInsert(ctx context.Context, tx *sql.Tx, md model.ModelDeclaration) (*sql.Stmt, error) {
	cols := exportableColumns(md)
	if len(cols) == 0 {
		return nil, fmt.Errorf("model %q has no importable columns", md.Name)
	}

	quotedCols := make([]string, len(cols))
	placeholders := make([]string, len(cols))
	for i, c := range cols {
		quotedCols[i] = quoteIdent(c)
		placeholders[i] = fmt.Sprintf("$%d", i+1)
	}

	sqlStr := fmt.Sprintf("INSERT INTO %s (%s) VALUES (%s) ON CONFLICT DO NOTHING",
		quoteIdent(modeltable.Name(md)), strings.Join(quotedCols, ", "), strings.Join(placeholders, ", "))
	return tx.PrepareContext(ctx, sqlStr)
}

func insertArgs(md model.ModelDeclaration, record map[string]any) ([]any, error) {
	cols := exportableColumns(md)
	args := make([]any, len(cols))
	for i, c := range cols {
		v, err := sqlValue(record[c])
		if err != nil {
			return nil, fmt.Errorf("column %q: %w", c, err)
		}
		args[i] = v
	}
	return args, nil
}

// JSON numbers retain their text to avoid rounding bigint values through float64.
type importNumber string

func (n importNumber) String() string { return string(n) }

func (n importNumber) MarshalJSON() ([]byte, error) {
	return []byte(n), nil
}

func (n importNumber) Int64() (int64, error) {
	return strconv.ParseInt(string(n), 10, 64)
}

func (n importNumber) Float64() (float64, error) {
	return strconv.ParseFloat(string(n), 64)
}

// Unsupported kinds fall back to recursive decoding, preserving numbers at every depth.
var numberPreservingUnmarshalers = json.UnmarshalFromFunc(func(dec *jsontext.Decoder, v *any) error {
	if dec.PeekKind() != '0' {
		return errors.ErrUnsupported
	}
	tok, err := dec.ReadToken()
	if err != nil {
		return err
	}
	*v = importNumber(tok.String())
	return nil
})

func sqlValue(v any) (any, error) {
	switch vv := v.(type) {
	case importNumber:
		if i, err := vv.Int64(); err == nil {
			return i, nil
		}
		f, err := vv.Float64()
		if err != nil {
			return nil, fmt.Errorf("decode number %q: %w", vv, err)
		}
		return f, nil
	case float64:
		return vv, nil
	case map[string]any, []any:
		b, err := json.Marshal(vv, json.Deterministic(true))
		if err != nil {
			return nil, fmt.Errorf("re-encode JSON value: %w", err)
		}
		return string(b), nil
	default:
		return v, nil
	}
}

func beginTenantScopedWrite(ctx context.Context, db *sql.DB, tenantSlug string) (*sql.Tx, error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin transaction: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `SELECT set_config('search_path', $1, true)`, "tenant_"+tenantSlug+", public"); err != nil {
		_ = tx.Rollback()
		return nil, fmt.Errorf("set search_path: %w", err)
	}
	return tx, nil
}

type loadIdentities struct {
	Mapping  map[string]string
	TenantID string
}
