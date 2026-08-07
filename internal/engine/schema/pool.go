package schema

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"hash/fnv"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/manifest"
)

const createModuleSchemaVersionsSchema = `CREATE SCHEMA IF NOT EXISTS system`

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

func (p *SchemaSyncPool) Bootstrap(ctx context.Context) error {
	if _, err := p.primary.ExecContext(ctx, createModuleSchemaVersionsSchema); err != nil {
		return fmt.Errorf("create system schema: %w", err)
	}
	if _, err := p.primary.ExecContext(ctx, createModuleSchemaVersionsTable); err != nil {
		return fmt.Errorf("create module_schema_versions table: %w", err)
	}

	return nil
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
