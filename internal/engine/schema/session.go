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
	// readTx is set only for a session BeginRead opened — every read runs
	// inside this one REPEATABLE READ read-only transaction rather than
	// directly against conn, so Diff's multiple statements (inspect, then
	// diff) see one fixed MVCC snapshot instead of READ COMMITTED's
	// default per-statement snapshot, which could otherwise straddle a
	// concurrent sync's own non-transactional (CREATE/DROP INDEX
	// CONCURRENTLY) and transactional DDL and see a half-applied schema.
	// No pg_advisory_lock is ever taken for this session, so Close has no
	// lock to release — only readTx to commit (read-only, so COMMIT vs.
	// ROLLBACK make no difference) and the connection to close.
	readTx *sql.Tx
}

// execQuerier is ariga.io/atlas/sql/schema.ExecQuerier's method set,
// declared locally so this package's own callers (diff.go, apply.go's
// execWithRetry) don't need to import atlas just to pick between s.conn
// and s.readTx, or between *sql.Conn and *sql.Tx — all of which already
// satisfy it structurally.
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

// Close releases the session's connection, and its pg_advisory_lock too
// unless this session came from BeginRead (which never took one, and
// instead has a readTx to close out first).
func (s *SchemaSyncSession) Close(ctx context.Context) error {
	if s.readTx != nil {
		commitErr := s.readTx.Commit() // read-only; commit vs. rollback is equivalent
		closeErr := s.conn.Close()
		if commitErr != nil {
			return commitErr
		}
		return closeErr
	}
	lockA, lockB := AdvisoryLockKeys(s.tenantSlug, s.moduleName)
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

// RecordSyncSuccess upserts this session's (tenant, module) row to the
// module's current manifest version, "ok" status, and now as
// schema_synced_at — what NeedsSync's skip-check reads on the next sync
// attempt (multitenancy-internals.md §16 "Tracking sync state").
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

// RecordSyncFailure marks this session's (tenant, module) row "failed"
// without touching current_version/schema_synced_at, so a failed upgrade
// doesn't claim the new version is live. Deliberately a plain UPDATE, not
// an upsert: if no row exists yet (this tenant/module has never
// successfully synced), there is nothing to mark failed — NeedsSync will
// keep reporting true and retry on the next sync pass, which is exactly
// what a never-yet-synced pair should do.
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
