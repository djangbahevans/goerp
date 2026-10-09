package schema

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"hash/fnv"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/db"
	"github.com/djangbahevans/goerp/internal/engine/manifest"
)

// ModuleSyncStatus is one module's sync record for one tenant — a row of
// system.module_schema_versions.
type ModuleSyncStatus struct {
	ModuleName     string     `json:"module_name"`
	CurrentVersion string     `json:"current_version"`
	Status         string     `json:"status"` // "ok" | "failed" | "in_progress"
	SyncedAt       *time.Time `json:"synced_at,omitempty"`
}

// nullTimeToPtr converts a scanned nullable timestamp into the *time.Time
// shape ModuleSyncStatus/TenantModuleStatus's own SyncedAt field uses —
// StatusForTenant's and StatusFiltered's row-scan loops both do this
// conversion independently; their two SELECTs otherwise scan structurally
// different column sets (StatusFiltered's own tenant-identity and
// data-migration columns have no StatusForTenant equivalent), so this
// conversion is the piece actually shared between them, not the scan
// itself.
func nullTimeToPtr(nt sql.NullTime) *time.Time {
	if !nt.Valid {
		return nil
	}
	return &nt.Time
}

const createModuleSchemaVersionsTable = `
CREATE TABLE IF NOT EXISTS system.module_schema_versions (
    tenant_id               UUID        NOT NULL,
    module_name             TEXT        NOT NULL,
    current_version         TEXT        NOT NULL,
    schema_synced_at        TIMESTAMPTZ,
    schema_sync_status      TEXT        NOT NULL DEFAULT 'in_progress',
    data_migration_version  TEXT,
    data_migration_status   TEXT,
    PRIMARY KEY (tenant_id, module_name)
)
`

// Pending validations record NOT VALID constraints for background validation. The stored
// tenant slug avoids a separate lookup when setting search_path.
const createPendingConstraintValidationsTable = `
CREATE TABLE IF NOT EXISTS system.pending_constraint_validations (
    tenant_id        UUID        NOT NULL,
    tenant_slug      TEXT        NOT NULL,
    table_name       TEXT        NOT NULL,
    constraint_name  TEXT        NOT NULL,
    status           TEXT        NOT NULL DEFAULT 'pending',
    error            TEXT,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    validated_at     TIMESTAMPTZ,
    PRIMARY KEY (tenant_id, table_name, constraint_name)
)
`

// Acceptance rows retain an audit trail after consumption. Version pinning and consumed_at
// prevent structurally identical future changes from reusing stale operator consent.
const createSchemaSyncAcceptancesTable = `
CREATE TABLE IF NOT EXISTS system.schema_sync_acceptances (
    id             UUID PRIMARY KEY DEFAULT uuidv7(),
    tenant_id      UUID NOT NULL,
    module_name    TEXT NOT NULL,
    module_version TEXT NOT NULL,
    target_hash    TEXT NOT NULL,
    reason         TEXT NOT NULL,
    operator       TEXT NOT NULL,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    consumed_at    TIMESTAMPTZ
)
`

const createSchemaSyncAcceptancesUnconsumedIndex = `
CREATE UNIQUE INDEX IF NOT EXISTS schema_sync_acceptances_unconsumed_idx
    ON system.schema_sync_acceptances (tenant_id, module_name, module_version, target_hash)
    WHERE consumed_at IS NULL
`

type SchemaSyncPool struct {
	primary            *sql.DB
	lockAcquireTimeout time.Duration
}

const createModelExtensionObjects = `
CREATE TABLE IF NOT EXISTS system.model_extension_objects (
    tenant_id UUID NOT NULL,
    table_name TEXT NOT NULL,
    object_kind TEXT NOT NULL,
    object_name TEXT NOT NULL,
    extension_module TEXT NOT NULL,
    PRIMARY KEY (tenant_id, table_name, object_kind, object_name)
)`

func NewPool(pool *sql.DB, lockAcquireTimeout time.Duration) *SchemaSyncPool {
	return &SchemaSyncPool{primary: pool, lockAcquireTimeout: lockAcquireTimeout}
}

// Raw returns the schema_sync_user pool with BYPASSRLS privileges for DDL and
// administrative bulk operations. It must not serve ordinary user-scoped reads.
func (p *SchemaSyncPool) Raw() *sql.DB {
	return p.primary
}

// Bootstrap creates schema-sync metadata under an advisory lock to serialize concurrent
// callers.
func (p *SchemaSyncPool) Bootstrap(ctx context.Context) error {
	keys := []int64{db.SystemSchemaLockKey, db.AdvisoryLockKey("schema.Bootstrap")}
	return db.WithAdvisoryLock(ctx, p.primary, keys, func(tx *sql.Tx) error {
		if err := db.EnsureSystemSchema(ctx, tx); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, createModuleSchemaVersionsTable); err != nil {
			return fmt.Errorf("create module_schema_versions table: %w", err)
		}
		if _, err := tx.ExecContext(ctx, createPendingConstraintValidationsTable); err != nil {
			return fmt.Errorf("create pending_constraint_validations table: %w", err)
		}
		if _, err := tx.ExecContext(ctx, createSchemaSyncAcceptancesTable); err != nil {
			return fmt.Errorf("create schema_sync_acceptances table: %w", err)
		}
		if _, err := tx.ExecContext(ctx, createSchemaSyncAcceptancesUnconsumedIndex); err != nil {
			return fmt.Errorf("create schema_sync_acceptances unconsumed index: %w", err)
		}
		if _, err := tx.ExecContext(ctx, createModelExtensionObjects); err != nil {
			return fmt.Errorf("create model_extension_objects: %w", err)
		}

		return nil
	})
}

// StatusForTenant returns every module_schema_versions row for the given
// tenant — the raw material for the "N of M modules synced" ratio
// GET /admin/tenants/{slug} reports (adminapi.SyncStatusReader). The
// denominator is deliberately "modules with a sync record for this
// tenant," not "every module currently loaded by the engine" — a module
// that has never attempted sync for this tenant has nothing meaningful to
// report either way, so this needs no dependency on a live module
// registry.
func (p *SchemaSyncPool) StatusForTenant(ctx context.Context, tenantID string) ([]ModuleSyncStatus, error) {
	rows, err := p.primary.QueryContext(ctx, `
		SELECT module_name, current_version, schema_sync_status, schema_synced_at
		FROM system.module_schema_versions
		WHERE tenant_id = $1
		ORDER BY module_name
	`, tenantID)
	if err != nil {
		return nil, fmt.Errorf("query module sync status: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var statuses []ModuleSyncStatus
	for rows.Next() {
		var s ModuleSyncStatus
		var syncedAt sql.NullTime
		if err := rows.Scan(&s.ModuleName, &s.CurrentVersion, &s.Status, &syncedAt); err != nil {
			return nil, fmt.Errorf("scan module sync status: %w", err)
		}
		s.SyncedAt = nullTimeToPtr(syncedAt)
		statuses = append(statuses, s)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate module sync status: %w", err)
	}

	return statuses, nil
}

// TenantModuleStatus joins schema-sync status to its tenant slug for cross-tenant
// administration.
type TenantModuleStatus struct {
	// TenantID is not part of GET /admin/schema/status's documented
	// response shape (json:"-") — it's carried through purely so
	// tenantsync.Admin.Status's filter=pending sweep can diff a candidate
	// row without a second per-row tenantStore.GetBySlug lookup, since
	// this query already joins system.tenants for the slug.
	TenantID             string     `json:"-"`
	TenantSlug           string     `json:"tenant"`
	ModuleName           string     `json:"module_name"`
	CurrentVersion       string     `json:"current_version"`
	Status               string     `json:"status"` // "ok" | "failed" | "in_progress"
	SyncedAt             *time.Time `json:"synced_at,omitempty"`
	DataMigrationVersion string     `json:"data_migration_version,omitempty"`
	DataMigrationStatus  string     `json:"data_migration_status,omitempty"` // "ok" | "running" | "failed" | ""
}

// StatusFiltered returns every module_schema_versions row joined to its
// tenant's slug, optionally narrowed by tenant slug / module name / a
// literal schema_sync_status value ("ok"/"failed"/"in_progress" — the
// only values ever written; "pending" per cli-reference.md §4 isn't a
// stored status at all, and is filtered by the caller after this call
// returns, since answering it needs a live Diff this package's own
// pool.go has no business running — see internal/engine/tenant/sync's
// Admin.Status).
func (p *SchemaSyncPool) StatusFiltered(ctx context.Context, tenantSlug, moduleName, status string) ([]TenantModuleStatus, error) {
	rows, err := p.primary.QueryContext(ctx, `
		SELECT t.id, t.slug, v.module_name, v.current_version, v.schema_sync_status, v.schema_synced_at,
		       v.data_migration_version, v.data_migration_status
		FROM system.module_schema_versions v
		JOIN system.tenants t ON t.id = v.tenant_id
		WHERE ($1 = '' OR t.slug = $1)
		  AND ($2 = '' OR v.module_name = $2)
		  AND ($3 = '' OR v.schema_sync_status = $3)
		ORDER BY t.slug, v.module_name
	`, tenantSlug, moduleName, status)
	if err != nil {
		return nil, fmt.Errorf("query schema sync status: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var statuses []TenantModuleStatus
	for rows.Next() {
		var s TenantModuleStatus
		var syncedAt sql.NullTime
		var dataMigrationVersion, dataMigrationStatus sql.NullString
		if err := rows.Scan(&s.TenantID, &s.TenantSlug, &s.ModuleName, &s.CurrentVersion, &s.Status, &syncedAt,
			&dataMigrationVersion, &dataMigrationStatus); err != nil {
			return nil, fmt.Errorf("scan schema sync status: %w", err)
		}
		s.SyncedAt = nullTimeToPtr(syncedAt)
		s.DataMigrationVersion = dataMigrationVersion.String
		s.DataMigrationStatus = dataMigrationStatus.String
		statuses = append(statuses, s)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate schema sync status: %w", err)
	}

	return statuses, nil
}

// DataMigrationVersion returns the separate data watermark or 0.0.0 when none exists. DDL
// can reach a version before its data handlers run.
func (p *SchemaSyncPool) DataMigrationVersion(ctx context.Context, tenantID, moduleName string) (string, error) {
	var version sql.NullString
	err := p.primary.QueryRowContext(ctx,
		"SELECT data_migration_version FROM system.module_schema_versions WHERE tenant_id = $1 AND module_name = $2",
		tenantID, moduleName,
	).Scan(&version)

	switch {
	case errors.Is(err, sql.ErrNoRows):
		return "0.0.0", nil
	case err != nil:
		return "", fmt.Errorf("read data migration watermark: %w", err)
	}

	if !version.Valid {
		return "0.0.0", nil
	}
	return version.String, nil
}

// DataMigrationWatermark permits enqueue only when schema sync succeeded at targetVersion.
// This prevents handlers from running before their required DDL exists.
func (p *SchemaSyncPool) DataMigrationWatermark(ctx context.Context, tenantID, moduleName, targetVersion string) (watermark string, eligible bool, err error) {
	var currentVersion, syncStatus string
	var dmVersion sql.NullString
	err = p.primary.QueryRowContext(ctx,
		"SELECT current_version, schema_sync_status, data_migration_version FROM system.module_schema_versions WHERE tenant_id = $1 AND module_name = $2",
		tenantID, moduleName,
	).Scan(&currentVersion, &syncStatus, &dmVersion)

	switch {
	case errors.Is(err, sql.ErrNoRows):
		return "0.0.0", false, nil
	case err != nil:
		return "", false, fmt.Errorf("read data migration watermark: %w", err)
	}

	if currentVersion != targetVersion || syncStatus != "ok" {
		return "0.0.0", false, nil
	}
	if !dmVersion.Valid {
		return "0.0.0", true, nil
	}
	return dmVersion.String, true, nil
}

// AdvanceDataMigrationVersion updates an existing synced row after handler success. A
// missing row is an error rather than a fabricated DDL watermark.
func (p *SchemaSyncPool) AdvanceDataMigrationVersion(ctx context.Context, tenantID, moduleName, toVersion string) error {
	result, err := p.primary.ExecContext(ctx, `
		UPDATE system.module_schema_versions
		SET data_migration_version = $1, data_migration_status = 'ok'
		WHERE tenant_id = $2 AND module_name = $3
	`, toVersion, tenantID, moduleName)
	if err != nil {
		return fmt.Errorf("advance data migration watermark: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("advance data migration watermark: %w", err)
	}
	if rows == 0 {
		// A missing version row must fail the job rather than report success without
		// advancing its watermark.
		return fmt.Errorf("advance data migration watermark: no module_schema_versions row for tenant %s module %s", tenantID, moduleName)
	}
	return nil
}

// TableCount returns the number of tables in the tenant's own Postgres
// schema (tenant_{slug}) — the schema table count cli-reference.md §5
// documents as part of `goerp tenant status`'s output. A tenant whose
// schema hasn't been created yet reports 0, the same as a real empty
// schema would — information_schema.tables simply has no matching rows.
func (p *SchemaSyncPool) TableCount(ctx context.Context, tenantSlug string) (int, error) {
	var count int
	err := p.primary.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = $1
	`, "tenant_"+tenantSlug).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("count tenant schema tables: %w", err)
	}

	return count, nil
}

func (p *SchemaSyncPool) BeginSync(ctx context.Context, tenantID, tenantSlug, moduleName string, manifest *manifest.Manifest) (*SchemaSyncSession, error) {
	conn, err := p.primary.Conn(ctx)
	if err != nil {
		return nil, fmt.Errorf("acquire schema sync connection: %w", err)
	}

	lockCtx, cancel := context.WithTimeout(ctx, p.lockAcquireTimeout)
	defer cancel()

	lockModule := moduleName
	if manifest.Type == "field_extension" && manifest.Schema.ExtendsModule != nil {
		lockModule = *manifest.Schema.ExtendsModule
	}
	lockA, lockB := AdvisoryLockKeys(tenantSlug, lockModule)
	if _, err := conn.ExecContext(lockCtx, "SELECT pg_advisory_lock($1, $2)", lockA, lockB); err != nil {
		_ = conn.Close()
		if errors.Is(lockCtx.Err(), context.DeadlineExceeded) {
			return nil, fmt.Errorf("timed out waiting for schema sync lock for %s/%s (another process is syncing this pair): %w", tenantSlug, moduleName, lockCtx.Err())
		}

		return nil, fmt.Errorf("acquire schema sync lock for %s/%s: %w", tenantSlug, moduleName, err)
	}

	if _, err := conn.ExecContext(ctx, fmt.Sprintf("SET search_path = tenant_%s", tenantSlug)); err != nil {
		_, _ = conn.ExecContext(ctx, "SELECT pg_advisory_unlock($1, $2)", lockA, lockB)
		_ = conn.Close()
		return nil, err
	}

	return &SchemaSyncSession{
		conn:       conn,
		tenantID:   tenantID,
		tenantSlug: tenantSlug,
		moduleName: moduleName,
		lockModule: lockModule,
		manifest:   manifest,
	}, nil
}

// BeginRead opens a REPEATABLE READ read-only transaction for diffing without the sync
// advisory lock. It performs no DDL and observes a consistent snapshot.
func (p *SchemaSyncPool) BeginRead(ctx context.Context, tenantID, tenantSlug, moduleName string, manifest *manifest.Manifest) (*SchemaSyncSession, error) {
	conn, err := p.primary.Conn(ctx)
	if err != nil {
		return nil, fmt.Errorf("acquire schema read connection: %w", err)
	}

	if _, err := conn.ExecContext(ctx, fmt.Sprintf("SET search_path = tenant_%s", tenantSlug)); err != nil {
		_ = conn.Close()
		return nil, err
	}

	tx, err := conn.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("begin read-only schema snapshot: %w", err)
	}

	return &SchemaSyncSession{
		conn:       conn,
		tenantID:   tenantID,
		tenantSlug: tenantSlug,
		moduleName: moduleName,
		manifest:   manifest,
		readTx:     tx,
	}, nil
}

// AdvisoryLockKeys identifies the tenant/module DDL lock. Migration DDL derives the same
// keys separately to avoid a package import cycle; cross-check tests keep them aligned.
func AdvisoryLockKeys(tenantSlug, moduleName string) (int32, int32) {
	h := fnv.New32a()
	h.Write([]byte(tenantSlug))
	a := int32(h.Sum32())
	h.Reset()
	h.Write([]byte(moduleName))
	b := int32(h.Sum32())
	return a, b
}
