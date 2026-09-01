package tenantimport

import (
	"bufio"
	"bytes"
	"context"
	"database/sql"
	jsonv1 "encoding/json"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/Masterminds/semver/v3"
	"github.com/djangbahevans/goerp/internal/engine/auth/rowcrypt"
	"github.com/djangbahevans/goerp/internal/engine/checkpoint"
	"github.com/djangbahevans/goerp/internal/engine/module"
	"github.com/djangbahevans/goerp/internal/engine/registry"
	"github.com/djangbahevans/goerp/internal/engine/storage"
	"github.com/djangbahevans/goerp/internal/engine/tenant"
	tenantprovision "github.com/djangbahevans/goerp/internal/engine/tenant/provision"
	"github.com/djangbahevans/goerp/sdk/go/model"
	"github.com/riverqueue/river"
)

// defaultLeaseStaleAfter matches tenantexport's own constant of the same
// name/value — a module's full bulk load is one uninterrupted operation
// with no per-batch heartbeat renewal in this implementation, same as
// export's per-module dump.
const defaultLeaseStaleAfter = 30 * time.Minute

// Worker runs Args: downloads and decrypts the archive InputRef points at,
// checks every named module's version against what's currently loaded,
// creates NewSlug as a brand-new tenant, provisions its schema (engine
// tables plus each in-scope module's own schema), then bulk-loads each
// module's rows — checkpointed per module via internal/engine/checkpoint,
// same resumability mechanism and re-invoked-from-scratch-on-retry
// tolerance tenantexport.Worker documents for its own per-module loop.
// Never invited an admin user or seeded system data/default roles here:
// those are superseded by the archive's own imported rows, not something
// this ticket's scope reconstructs (see cli-reference.md's `tenant import`
// section).
type Worker struct {
	river.WorkerDefaults[Args]

	TenantStore *tenant.Store
	Registry    *registry.ModuleRegistry
	// RawDB is schema_sync_user's connection pool (schema.SchemaSyncPool.Raw)
	// — the same BYPASSRLS pool tenantexport.Worker reads through, used here
	// for the bulk INSERT write path. There is no live caller to enforce
	// field-security/RLS against: the archive's own rows already went
	// through export's field-security exclusion.
	RawDB          *sql.DB
	Checkpoints    *checkpoint.Store
	StorageBackend storage.Backend
	Provision      *tenantprovision.Activities
	// Keys decrypts Args.DecryptionKey — Importer.StartImport encrypted it
	// with the same RowKeySet before this job was ever inserted (goerp#450).
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
// context — same split tenantexport.Worker.run documents.
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

	snap := w.Registry.Snapshot()
	if snap == nil {
		return Result{}, fmt.Errorf("module registry not ready")
	}
	if err := checkModuleVersions(man, snap.Modules()); err != nil {
		return Result{}, err
	}

	t, err := w.resolveTenant(ctx, a.NewSlug)
	if err != nil {
		return Result{}, err
	}

	result, err := w.provisionAndLoad(ctx, job, t, man, moduleData, snap)
	if err != nil {
		// job.Attempt == job.MaxAttempts means River discards this job after
		// this failure — no further retry will ever get another chance to
		// finish provisioning t. Release its slug reservation now (the same
		// compensating action tenantprovision.Workflow's own failed-schema-
		// creation path takes, ReleaseSlugReservation ->
		// tenant.Store.DeleteProvisioning) so the slug isn't stuck forever.
		// ErrTenantNotFound here just means t already moved past
		// StatusProvisioning (e.g. a concurrent attempt finished first) —
		// nothing to release.
		if job.Attempt >= job.MaxAttempts {
			if relErr := w.TenantStore.DeleteProvisioning(ctx, t.ID); relErr != nil && !errors.Is(relErr, tenant.ErrTenantNotFound) {
				return Result{}, fmt.Errorf("%w (also failed to release slug %q after final attempt: %v)", err, a.NewSlug, relErr)
			}
		}
		return Result{}, err
	}
	return result, nil
}

// provisionAndLoad runs every step from schema provisioning through
// activation for tenant t — split out from run so run can wrap it with
// slug-release compensation on a final failed attempt (see run's own
// comment above the call site).
func (w *Worker) provisionAndLoad(ctx context.Context, job *river.Job[Args], t *tenant.Tenant, man manifest, moduleData map[string][]byte, snap *registry.RegistrySnapshot) (Result, error) {
	if err := w.Provision.CreateTenantSchema(ctx, t.Slug); err != nil {
		return Result{}, fmt.Errorf("create tenant schema: %w", err)
	}
	if err := w.Provision.CreateEngineTables(ctx, t.Slug); err != nil {
		return Result{}, fmt.Errorf("create engine tables: %w", err)
	}

	moduleNames := make([]string, len(man.Modules))
	for i, m := range man.Modules {
		moduleNames[i] = m.Name
		if err := w.Provision.SyncModuleSchema(ctx, t.ID, t.Slug, m.Name); err != nil {
			return Result{}, fmt.Errorf("sync schema for module %q: %w", m.Name, err)
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
				continue // a previous attempt already loaded this module
			}
			return Result{}, fmt.Errorf("acquire lease for module %q: %w", m.Name, err)
		}

		mod, ok := snap.Modules()[m.Name]
		if !ok {
			_ = progress.MarkFailed(ctx)
			return Result{}, fmt.Errorf("module %q is no longer loaded", m.Name)
		}

		if err := w.loadModule(ctx, t.Slug, mod, moduleData[m.Name]); err != nil {
			_ = progress.MarkFailed(ctx)
			return Result{}, fmt.Errorf("load module %q: %w", m.Name, err)
		}
		if err := progress.MarkComplete(ctx); err != nil {
			return Result{}, fmt.Errorf("mark module %q complete: %w", m.Name, err)
		}
	}

	if err := w.Provision.ActivateTenant(ctx, t.Slug); err != nil {
		return Result{}, fmt.Errorf("activate tenant: %w", err)
	}

	return Result{TenantID: t.ID, TenantSlug: t.Slug, ModulesImported: moduleNames}, nil
}

// resolveTenant returns the existing StatusProvisioning tenant for slug (a
// retried attempt picking up after a previous one already created it), or
// creates it fresh. A tenant found in any other status is a genuine
// conflict — most likely the slug is already in use by an unrelated
// tenant — and is reported as an error rather than silently reused.
func (w *Worker) resolveTenant(ctx context.Context, slug string) (*tenant.Tenant, error) {
	t, err := w.TenantStore.GetBySlug(ctx, slug)
	if err == nil {
		if t.Status != tenant.StatusProvisioning {
			return nil, fmt.Errorf("tenant %q already exists (status=%s)", slug, t.Status)
		}
		return t, nil
	}
	if !errors.Is(err, tenant.ErrTenantNotFound) {
		return nil, fmt.Errorf("look up tenant %q: %w", slug, err)
	}

	t, err = w.TenantStore.CreateTenant(ctx, slug, slug)
	if err != nil {
		return nil, fmt.Errorf("create tenant %q: %w", slug, err)
	}
	return t, nil
}

// checkModuleVersions requires every module the archive names to be
// currently loaded at exactly the exported version — collecting every
// missing/mismatched module@version pair into one error rather than
// failing on the first, per cli-reference.md's `tenant import` acceptance
// criteria.
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

// loadModule bulk-inserts every row data's JSONL lines declare, one INSERT
// per line, batched inside a single write transaction per module. "ON
// CONFLICT DO NOTHING" (no target needed — Postgres applies a bare clause
// to any conflicting constraint) makes a re-run after a partial failure
// idempotent: rows already inserted by the interrupted attempt are
// silently skipped rather than duplicated or erroring.
func (w *Worker) loadModule(ctx context.Context, tenantSlug string, mod *module.LoadedModule, data []byte) error {
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
		quoteIdent(tableNameFor(md)), strings.Join(quotedCols, ", "), strings.Join(placeholders, ", "))
	return tx.PrepareContext(ctx, sqlStr)
}

// insertArgs returns args in exportableColumns(md)'s own order, converting
// each field's decoded JSON value into something database/sql's pgx driver
// can bind: a JSON number becomes an int64 (falling back to float64 for a
// non-integral value) rather than always float64 (avoiding precision loss
// on a bigint/id column), and a nested object/array (a JSON/JSONB-typed
// field) is re-marshaled to its raw JSON text, since the driver has no
// built-in encoding for a bare Go map/slice.
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

// numberPreservingUnmarshalers restores v1's Decoder.UseNumber() behavior
// for a map[string]any/[]any-typed decode target — sqlValue below needs a
// JSON number as jsonv1.Number, not v2's own any-decode default of
// float64, to avoid precision loss on a bigint/id column. Falling back to
// errors.ErrUnsupported for every other kind lets v2's own any-decoder
// recurse through objects/arrays, re-invoking this same unmarshaler at
// every nesting depth.
var numberPreservingUnmarshalers = json.UnmarshalFromFunc(func(dec *jsontext.Decoder, v *any) error {
	if dec.PeekKind() != '0' {
		return errors.ErrUnsupported
	}
	tok, err := dec.ReadToken()
	if err != nil {
		return err
	}
	*v = jsonv1.Number(tok.String())
	return nil
})

func sqlValue(v any) (any, error) {
	switch vv := v.(type) {
	case jsonv1.Number:
		if i, err := vv.Int64(); err == nil {
			return i, nil
		}
		f, err := vv.Float64()
		if err != nil {
			return nil, fmt.Errorf("decode number %q: %w", vv.String(), err)
		}
		return f, nil
	case float64:
		return vv, nil
	case map[string]any, []any:
		// Deterministic sorts a map's keys — matches v1's Marshal default,
		// in case the target column is json rather than jsonb (which would
		// otherwise re-normalize key order on its own).
		b, err := json.Marshal(vv, json.Deterministic(true))
		if err != nil {
			return nil, fmt.Errorf("re-encode JSON value: %w", err)
		}
		return string(b), nil
	default:
		return v, nil
	}
}

// beginTenantScopedWrite mirrors tenantexport's own beginTenantScopedRead,
// but read-write: no app.current_user_* session variables to set, same
// reasoning as that function's own doc comment (no live caller for RLS
// policies to evaluate against, and this connection bypasses them
// entirely).
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
