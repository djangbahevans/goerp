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

type SchemaSyncPool struct {
	primary            *sql.DB
	lockAcquireTimeout time.Duration
}

func NewPool(pool *sql.DB, lockAcquireTimeout time.Duration) *SchemaSyncPool {
	return &SchemaSyncPool{primary: pool, lockAcquireTimeout: lockAcquireTimeout}
}

// Bootstrap creates system.module_schema_versions if it doesn't already
// exist. Concurrent-safe against other processes calling Bootstrap at the
// same time (goerp#171) via db.WithAdvisoryLock.
func (p *SchemaSyncPool) Bootstrap(ctx context.Context) error {
	keys := []int64{db.SystemSchemaLockKey, db.AdvisoryLockKey("schema.Bootstrap")}
	return db.WithAdvisoryLock(ctx, p.primary, keys, func(tx *sql.Tx) error {
		if err := db.EnsureSystemSchema(ctx, tx); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, createModuleSchemaVersionsTable); err != nil {
			return fmt.Errorf("create module_schema_versions table: %w", err)
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
		if syncedAt.Valid {
			s.SyncedAt = &syncedAt.Time
		}
		statuses = append(statuses, s)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate module sync status: %w", err)
	}

	return statuses, nil
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

	lockA, lockB := advisoryLockKeys(tenantSlug, moduleName)
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
		manifest:   manifest,
	}, nil
}

func advisoryLockKeys(tenantSlug, moduleName string) (int32, int32) {
	h := fnv.New32a()
	h.Write([]byte(tenantSlug))
	a := int32(h.Sum32())
	h.Reset()
	h.Write([]byte(moduleName))
	b := int32(h.Sum32())
	return a, b
}
