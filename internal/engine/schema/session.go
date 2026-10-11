package schema

import (
	"cmp"
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/djangbahevans/goerp/internal/engine/manifest"
)

type SchemaSyncSession struct {
	conn       *sql.Conn
	tenantID   string
	tenantSlug string
	moduleName string
	lockModule string
	manifest   *manifest.Manifest
	// Repeatable-read sessions keep schema inspection and diffing on one snapshot,
	// preventing concurrent DDL from exposing a partially applied schema.
	readTx *sql.Tx
}

type execQuerier interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

func (s *SchemaSyncSession) execQuerier() execQuerier {
	if s.readTx != nil {
		return s.readTx
	}
	return s.conn
}

func (s *SchemaSyncSession) Close(ctx context.Context) error {
	if s.readTx != nil {
		commitErr := s.readTx.Commit()
		closeErr := s.conn.Close()
		if commitErr != nil {
			return commitErr
		}
		return closeErr
	}
	lockA, lockB := AdvisoryLockKeys(s.tenantSlug, cmp.Or(s.lockModule, s.moduleName))
	_, unlockErr := s.conn.ExecContext(ctx, "SELECT pg_advisory_unlock($1, $2)", lockA, lockB)
	closeErr := s.conn.Close()
	if unlockErr != nil {
		return unlockErr
	}
	return closeErr
}

func (s *SchemaSyncSession) OwnedModels() []string {
	return s.manifest.Schema.OwnedModels
}

func (s *SchemaSyncSession) ExtendsModule() *string {
	return s.manifest.Schema.ExtendsModule
}

func (s *SchemaSyncSession) ExtendsModels() []string {
	return s.manifest.Schema.ExtendsModels
}

func (s *SchemaSyncSession) HasDataMigrations() bool {
	return s.manifest.Schema.HasDataMigrations
}

func (s *SchemaSyncSession) ModuleVersion() string {
	return s.manifest.Version
}

func (s *SchemaSyncSession) NeedsSync(ctx context.Context) (bool, error) {
	var lastSynced string
	err := s.conn.QueryRowContext(ctx,
		"SELECT current_version FROM system.module_schema_versions WHERE tenant_id = $1 AND module_name = $2",
		s.tenantID, s.moduleName,
	).Scan(&lastSynced)

	switch {
	case errors.Is(err, sql.ErrNoRows):
		return true, nil
	case err != nil:
		return false, fmt.Errorf("check module schema version: %w", err)
	}

	return !shouldSkipSync(s.manifest.Version, lastSynced), nil
}

// InitialInstall checks for a successful sync while the session holds the tenant/module
// lock, so concurrent syncs cannot both classify an upgrade as a first installation.
func (s *SchemaSyncSession) InitialInstall(ctx context.Context) (bool, error) {
	var installed bool
	err := s.conn.QueryRowContext(ctx,
		"SELECT EXISTS (SELECT 1 FROM system.module_schema_versions WHERE tenant_id = $1 AND module_name = $2)",
		s.tenantID, s.moduleName,
	).Scan(&installed)
	if err != nil {
		return false, fmt.Errorf("check initial module installation: %w", err)
	}

	return !installed, nil
}

func (s *SchemaSyncSession) RecordSyncSuccess(ctx context.Context) error {
	_, err := s.conn.ExecContext(ctx, `
		INSERT INTO system.module_schema_versions (tenant_id, module_name, current_version, schema_synced_at, schema_sync_status)
		VALUES ($1, $2, $3, NOW(), 'ok')
		ON CONFLICT (tenant_id, module_name) DO UPDATE SET
			current_version = EXCLUDED.current_version,
			schema_synced_at = EXCLUDED.schema_synced_at,
			schema_sync_status = 'ok'
	`, s.tenantID, s.moduleName, s.manifest.Version)
	if err != nil {
		return fmt.Errorf("record sync success: %w", err)
	}
	return nil
}

// Failed upgrades retain their installed version; failed initial syncs leave no row,
// so retries remain eligible for first-install seeding.
func (s *SchemaSyncSession) RecordSyncFailure(ctx context.Context) error {
	_, err := s.conn.ExecContext(ctx,
		`UPDATE system.module_schema_versions SET schema_sync_status = 'failed' WHERE tenant_id = $1 AND module_name = $2`,
		s.tenantID, s.moduleName,
	)
	if err != nil {
		return fmt.Errorf("record sync failure: %w", err)
	}
	return nil
}
