package schema

import (
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
	manifest   *manifest.Manifest
}

func (s *SchemaSyncSession) Close(ctx context.Context) error {
	lockA, lockB := advisoryLockKeys(s.tenantSlug, s.moduleName)
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
