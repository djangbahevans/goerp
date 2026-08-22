// Package tenantconfig owns per-tenant config key/value overrides —
// manifest-spec.md §17's "Config Schema" runtime storage. Keys are
// already fully namespaced by the time they reach this package
// ({module}.{key}, or the engine's own reserved "engine." namespace,
// e.g. "engine.mfa_mode") — this package has no knowledge of manifests
// or module names, only opaque key strings.
//
// Settings-UI auto-generation, the full 8-type schema, encrypted/
// generated/public field metadata, and validation_regex enforcement are
// separate, larger scope for a later pass once a module actually
// declares a config_schema (manifest-spec.md §17) — this package is
// only the minimal key/value store and read/write path goerp#308 (MFA
// enforcement modes) needs.
package tenantconfig

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/djangbahevans/goerp/internal/engine/db"
)

const createTenantConfigOverridesTable = `
CREATE TABLE IF NOT EXISTS system.tenant_config_overrides (
    tenant_id  UUID NOT NULL REFERENCES system.tenants(id) ON DELETE CASCADE,
    key        TEXT NOT NULL,
    value      TEXT NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (tenant_id, key)
)
`

type Store struct {
	db *sql.DB
}

func NewStore(db *sql.DB) *Store {
	return &Store{db: db}
}

// Bootstrap creates system.tenant_config_overrides if it doesn't already
// exist. Idempotent and concurrent-safe against other processes calling
// Bootstrap at the same time, same convention tenant.Store.Bootstrap uses
// (goerp#171).
func (s *Store) Bootstrap(ctx context.Context) error {
	keys := []int64{db.SystemSchemaLockKey, db.AdvisoryLockKey("tenantconfig.Bootstrap")}
	return db.WithAdvisoryLock(ctx, s.db, keys, func(tx *sql.Tx) error {
		if err := db.EnsureSystemSchema(ctx, tx); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, createTenantConfigOverridesTable); err != nil {
			return fmt.Errorf("create tenant_config_overrides table: %w", err)
		}
		return nil
	})
}

// Get returns key's value for tenantID. ok is false, with a nil error,
// when the key isn't set — a cache miss isn't itself an error, so the
// caller supplies its own default (manifest-spec.md §17's `default`
// field, at the layer above this package).
func (s *Store) Get(ctx context.Context, tenantID, key string) (value string, ok bool, err error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT value FROM system.tenant_config_overrides WHERE tenant_id = $1 AND key = $2
	`, tenantID, key)

	if err := row.Scan(&value); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", false, nil
		}
		return "", false, fmt.Errorf("get tenant config value: %w", err)
	}
	return value, true, nil
}

// Set upserts key's value for tenantID.
func (s *Store) Set(ctx context.Context, tenantID, key, value string) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO system.tenant_config_overrides (tenant_id, key, value)
		VALUES ($1, $2, $3)
		ON CONFLICT (tenant_id, key) DO UPDATE SET value = $3, updated_at = NOW()
	`, tenantID, key, value)
	if err != nil {
		return fmt.Errorf("set tenant config value: %w", err)
	}
	return nil
}
