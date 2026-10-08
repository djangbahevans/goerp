// Package tenantconfig stores namespaced tenant configuration overrides. Resolver layers
// overrides, tenant values and manifest seeds; Listener invalidates cached values across
// instances through Postgres notifications.
package tenantconfig

import (
	"context"
	"database/sql"
	"encoding/json/v2"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

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

// Bootstrap creates tenant configuration overrides under an advisory lock to serialize
// concurrent callers.
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

// Password policy keys (auth-internals.md §3 "Password policy at
// sign-in"): a write that changes a key in changeTracked stamps its
// timestamp key with the time of the change in the same transaction, so
// the timestamp can't drift from the values it dates.
const (
	PasswordPolicyPrefix         = "auth.password_policy."
	PasswordPolicyMinLengthKey   = PasswordPolicyPrefix + "min_length"
	PasswordPolicyEnforcementKey = PasswordPolicyPrefix + "enforcement"
	PasswordPolicyGraceDaysKey   = PasswordPolicyPrefix + "grace_days"
	PasswordPolicyChangedAtKey   = PasswordPolicyPrefix + "changed_at"
)

// changeTracked maps a key to the timestamp key a change to it stamps.
var changeTracked = map[string]string{
	PasswordPolicyMinLengthKey:   PasswordPolicyChangedAtKey,
	PasswordPolicyEnforcementKey: PasswordPolicyChangedAtKey,
}

// ErrReadOnlyKey rejects a direct write to a key the engine maintains.
var ErrReadOnlyKey = errors.New("config key is maintained by the engine and cannot be set directly")

func isReadOnly(key string) bool {
	for _, stamp := range changeTracked {
		if key == stamp {
			return true
		}
	}
	return false
}

// GetPrefix returns every key set for tenantID that starts with prefix.
func (s *Store) GetPrefix(ctx context.Context, tenantID, prefix string) (map[string]string, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT key, value FROM system.tenant_config_overrides
		WHERE tenant_id = $1 AND starts_with(key, $2)
	`, tenantID, prefix)
	if err != nil {
		return nil, fmt.Errorf("get tenant config prefix: %w", err)
	}
	defer func() { _ = rows.Close() }()

	values := map[string]string{}
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return nil, fmt.Errorf("scan tenant config value: %w", err)
		}
		values[k] = v
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("get tenant config prefix: %w", err)
	}
	return values, nil
}

// Set broadcasts cache invalidation only after a successful commit. Change-tracked
// timestamps advance only when the value changes.
func (s *Store) Set(ctx context.Context, tenantID, key, value string) error {
	return s.SetMany(ctx, tenantID, map[string]string{key: value})
}

// SetMany is Set for several keys in one transaction: every value lands,
// or none does, and a timestamp is stamped at most once however many of
// its keys change.
func (s *Store) SetMany(ctx context.Context, tenantID string, values map[string]string) error {
	if len(values) == 0 {
		return nil
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		if isReadOnly(key) {
			return ErrReadOnlyKey
		}
		keys = append(keys, key)
	}
	// A fixed write order keeps two concurrent SetMany calls from taking
	// row locks in opposite orders.
	slices.Sort(keys)

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin set tenant config value: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	stamp := map[string]bool{}
	for _, key := range keys {
		stampKey, tracked := changeTracked[key]
		if !tracked {
			continue
		}
		if _, locked := stamp[stampKey]; !locked {
			// Serializes writers of this tenant's tracked keys, so the
			// read below sees the committed value and every real change
			// stamps.
			lockKey := db.AdvisoryLockKey("tenantconfig.stamp:" + tenantID + ":" + stampKey)
			if _, err := tx.ExecContext(ctx, "SELECT pg_advisory_xact_lock($1)", lockKey); err != nil {
				return fmt.Errorf("lock tenant config %s: %w", stampKey, err)
			}
			stamp[stampKey] = false
		}
		previous, ok, err := getTx(ctx, tx, tenantID, key)
		if err != nil {
			return err
		}
		if !ok || previous != values[key] {
			stamp[stampKey] = true
		}
	}

	changed := slices.Clone(keys)
	for _, key := range keys {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO system.tenant_config_overrides (tenant_id, key, value)
			VALUES ($1, $2, $3)
			ON CONFLICT (tenant_id, key) DO UPDATE SET value = $3, updated_at = NOW()
		`, tenantID, key, values[key]); err != nil {
			return fmt.Errorf("set tenant config value: %w", err)
		}
	}

	for stampKey, changedUnder := range stamp {
		if !changedUnder {
			continue
		}
		// The database clock, like tenant_members.joined_at, which the
		// password policy deadline compares this against.
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO system.tenant_config_overrides (tenant_id, key, value)
			VALUES ($1, $2, to_char(NOW() AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS.US"Z"'))
			ON CONFLICT (tenant_id, key) DO UPDATE SET value = EXCLUDED.value, updated_at = NOW()
		`, tenantID, stampKey); err != nil {
			return fmt.Errorf("stamp tenant config %s: %w", stampKey, err)
		}
		changed = append(changed, stampKey)
	}

	for _, k := range changed {
		payload, err := json.Marshal(configChangedPayload{TenantID: tenantID, Key: k})
		if err != nil {
			return fmt.Errorf("encode config changed payload: %w", err)
		}
		if _, err := tx.ExecContext(ctx, "SELECT pg_notify($1, $2)", configChangedChannel, string(payload)); err != nil {
			return fmt.Errorf("notify config changed: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit set tenant config value: %w", err)
	}
	return nil
}

const upsertModuleConfigValue = `
INSERT INTO %s.module_config (module_name, key, value, value_type, encrypted, updated_by)
VALUES ($1, $2, $3, $4, $5, NULLIF($6, '')::uuid)
ON CONFLICT (module_name, key) DO UPDATE SET
    value = $3, value_type = $4, encrypted = $5, updated_by = NULLIF($6, '')::uuid, updated_at = NOW()
`

const deleteModuleConfigValue = `DELETE FROM %s.module_config WHERE module_name = $1 AND key = $2`

// ModuleConfigValue is one module_config write. Value is already the
// caller's chosen on-disk encoding; a nil Value deletes the row, so the
// key falls back to the lower tiers.
type ModuleConfigValue struct {
	Key       string
	Value     []byte
	Type      string
	Encrypted bool
}

// SetModuleConfig upserts moduleName.key's value into tenantSchema's own
// module_config table (the tenant-admin tier, distinct from Store.Set's
// operator-override tier) and broadcasts configChangedChannel so every
// Resolver drops its now-stale cache entry. This package stays ignorant
// of config_schema types and encryption. updatedBy empty stores SQL NULL.
func (s *Store) SetModuleConfig(ctx context.Context, tenantID, tenantSchema, moduleName, key string, value []byte, valueType string, encrypted bool, updatedBy string) error {
	return s.SetModuleConfigMany(ctx, tenantID, tenantSchema, moduleName, []ModuleConfigValue{{Key: key, Value: value, Type: valueType, Encrypted: encrypted}}, updatedBy)
}

// SetModuleConfigMany is SetModuleConfig for several keys in one
// transaction: every write lands, or none does.
func (s *Store) SetModuleConfigMany(ctx context.Context, tenantID, tenantSchema, moduleName string, values []ModuleConfigValue, updatedBy string) error {
	if len(values) == 0 {
		return nil
	}
	values = slices.SortedFunc(slices.Values(values), func(a, b ModuleConfigValue) int { return strings.Compare(a.Key, b.Key) })

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin set module config value: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	upsert := fmt.Sprintf(upsertModuleConfigValue, tenantSchema)
	del := fmt.Sprintf(deleteModuleConfigValue, tenantSchema)
	for _, v := range values {
		if v.Value == nil {
			if _, err := tx.ExecContext(ctx, del, moduleName, v.Key); err != nil {
				return fmt.Errorf("delete module config value %s: %w", v.Key, err)
			}
		} else if _, err := tx.ExecContext(ctx, upsert, moduleName, v.Key, v.Value, v.Type, v.Encrypted, updatedBy); err != nil {
			return fmt.Errorf("set module config value %s: %w", v.Key, err)
		}

		payload, err := json.Marshal(configChangedPayload{TenantID: tenantID, Key: moduleName + "." + v.Key})
		if err != nil {
			return fmt.Errorf("encode config changed payload: %w", err)
		}
		if _, err := tx.ExecContext(ctx, "SELECT pg_notify($1, $2)", configChangedChannel, string(payload)); err != nil {
			return fmt.Errorf("notify config changed: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit set module config value: %w", err)
	}
	return nil
}

func getTx(ctx context.Context, tx *sql.Tx, tenantID, key string) (string, bool, error) {
	var value string
	err := tx.QueryRowContext(ctx, `
		SELECT value FROM system.tenant_config_overrides WHERE tenant_id = $1 AND key = $2
	`, tenantID, key).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("get tenant config value: %w", err)
	}
	return value, true, nil
}

// ModuleConfigRow is one tenant-admin module_config row. Value is the stored
// JSONB text: the value itself for a plain row, a JSON string holding
// ciphertext for an encrypted one.
type ModuleConfigRow struct {
	Value     string
	Type      string
	Encrypted bool
	UpdatedAt time.Time
}

// ModuleConfigRows returns moduleName's stored module_config rows in
// tenantSchema, keyed by short key. Unlike Resolver.Get it ignores the
// operator-override and manifest-default tiers, so a missing key means the
// tenant admin has set nothing.
func (s *Store) ModuleConfigRows(ctx context.Context, tenantSchema, moduleName string) (map[string]ModuleConfigRow, error) {
	query := fmt.Sprintf(`SELECT key, value::text, value_type, encrypted, updated_at FROM %s.module_config WHERE module_name = $1`, tenantSchema)
	rows, err := s.db.QueryContext(ctx, query, moduleName)
	if err != nil {
		return nil, fmt.Errorf("query module config rows: %w", err)
	}
	defer func() { _ = rows.Close() }()

	out := map[string]ModuleConfigRow{}
	for rows.Next() {
		var key string
		var row ModuleConfigRow
		if err := rows.Scan(&key, &row.Value, &row.Type, &row.Encrypted, &row.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan module config row: %w", err)
		}
		out[key] = row
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate module config rows: %w", err)
	}
	return out, nil
}
