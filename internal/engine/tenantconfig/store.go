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
//
// Resolver (resolver.go) layers multitenancy-internals.md §7's own
// three-tier config resolution chain — operator override, tenant-admin-set
// module_config, manifest tenant_config_seeds default — over this store,
// fronted by a TTL-only in-memory cache. Listener (listener.go) is that
// cache's cross-instance counterpart: Store.Set broadcasts a Postgres
// NOTIFY on every write, and a Listener invalidates a Resolver's matching
// cache entry the moment it arrives, rather than waiting out the TTL.
package tenantconfig

import (
	"context"
	"database/sql"
	"encoding/json/v2"
	"errors"
	"fmt"

	"github.com/djangbahevans/goerp/internal/engine/db"
)

// configChangedChannel is the single Postgres NOTIFY channel every write
// broadcasts on and every Listener subscribes to — one channel for every
// tenant, with the affected tenant/key in the payload (configChangedPayload),
// rather than one channel per tenant: a per-tenant channel would need every
// already-running engine instance to issue a fresh LISTEN the moment a new
// tenant is provisioned, with no existing mechanism to trigger that.
const configChangedChannel = "config_changed"

// configChangedPayload is configChangedChannel's own NOTIFY payload —
// small enough to stay well under Postgres's 8000-byte pg_notify cap for
// any realistic tenant ID/key.
type configChangedPayload struct {
	TenantID string `json:"tenant_id"`
	Key      string `json:"key"`
}

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

// Set upserts key's value for tenantID and broadcasts configChangedChannel
// so every engine instance's Resolver cache drops its now-stale entry —
// Postgres defers a transactional NOTIFY's delivery until COMMIT (and
// drops it on rollback), so a failed upsert never broadcasts a change
// that didn't actually happen.
func (s *Store) Set(ctx context.Context, tenantID, key, value string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin set tenant config value: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO system.tenant_config_overrides (tenant_id, key, value)
		VALUES ($1, $2, $3)
		ON CONFLICT (tenant_id, key) DO UPDATE SET value = $3, updated_at = NOW()
	`, tenantID, key, value); err != nil {
		return fmt.Errorf("set tenant config value: %w", err)
	}

	payload, err := json.Marshal(configChangedPayload{TenantID: tenantID, Key: key})
	if err != nil {
		return fmt.Errorf("encode config changed payload: %w", err)
	}
	if _, err := tx.ExecContext(ctx, "SELECT pg_notify($1, $2)", configChangedChannel, string(payload)); err != nil {
		return fmt.Errorf("notify config changed: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit set tenant config value: %w", err)
	}
	return nil
}
