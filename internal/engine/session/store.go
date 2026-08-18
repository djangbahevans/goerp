// Package session bootstraps the system.sessions table and inserts a
// session's first row — the table backing JWT/refresh-token issuance,
// rotation, and revocation (auth-internals.md §4 "Session table").
// Rotation and revocation land with goerp#147.
package session

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/db"
)

// createSessionsTable matches auth-internals.md §4's schema exactly,
// except mfa_credential_id drops the documented "REFERENCES user_mfa(id)"
// — no system.user_mfa table exists in this repo yet (a separate, unfiled
// ticket). The column stays a plain UUID so that constraint can be added
// via ALTER TABLE once user_mfa lands, the same reasoning system.tenants'
// suspended_by column already applies to a users(id) FK that didn't exist
// yet when tenants was bootstrapped.
const createSessionsTable = `
CREATE TABLE IF NOT EXISTS system.sessions (
    id                UUID PRIMARY KEY DEFAULT uuidv7(),
    user_id           UUID NOT NULL REFERENCES system.users(id) ON DELETE CASCADE,
    tenant_id         UUID NOT NULL REFERENCES system.tenants(id) ON DELETE CASCADE,
    family_id         UUID NOT NULL,
    device_id         UUID NOT NULL,
    refresh_hash      TEXT NOT NULL,
    user_agent        TEXT,
    ip_address        INET,
    country_code      CHAR(2),
    created_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_active_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    expires_at        TIMESTAMPTZ NOT NULL,
    rotated_at        TIMESTAMPTZ,
    revoked_at        TIMESTAMPTZ,
    revoke_reason     TEXT,
    mfa_verified_at   TIMESTAMPTZ,
    mfa_method        TEXT,
    mfa_credential_id UUID
)
`

const createSessionsRefreshHashIndex = `
CREATE INDEX IF NOT EXISTS idx_sessions_refresh_hash ON system.sessions(refresh_hash)
    WHERE revoked_at IS NULL AND rotated_at IS NULL
`

const createSessionsUserIndex = `
CREATE INDEX IF NOT EXISTS idx_sessions_user ON system.sessions(user_id, tenant_id)
    WHERE revoked_at IS NULL
`

const createSessionsFamilyIndex = `
CREATE INDEX IF NOT EXISTS idx_sessions_family ON system.sessions(family_id)
`

type Store struct {
	db *sql.DB
}

func NewStore(db *sql.DB) *Store {
	return &Store{db: db}
}

// Bootstrap creates system.sessions and its indexes if they don't already
// exist. Idempotent — safe to call on every engine startup, same as
// auditlog.Store.Bootstrap. Concurrent-safe against other processes
// calling Bootstrap at the same time via db.WithAdvisoryLock.
func (s *Store) Bootstrap(ctx context.Context) error {
	keys := []int64{db.SystemSchemaLockKey, db.AdvisoryLockKey("session.Bootstrap")}
	return db.WithAdvisoryLock(ctx, s.db, keys, func(tx *sql.Tx) error {
		if err := db.EnsureSystemSchema(ctx, tx); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, createSessionsTable); err != nil {
			return fmt.Errorf("create sessions table: %w", err)
		}
		if _, err := tx.ExecContext(ctx, createSessionsRefreshHashIndex); err != nil {
			return fmt.Errorf("create sessions refresh_hash index: %w", err)
		}
		if _, err := tx.ExecContext(ctx, createSessionsUserIndex); err != nil {
			return fmt.Errorf("create sessions user index: %w", err)
		}
		if _, err := tx.ExecContext(ctx, createSessionsFamilyIndex); err != nil {
			return fmt.Errorf("create sessions family index: %w", err)
		}

		return nil
	})
}

// Row is one session's insertable fields — a login event's first row in a
// fresh family (id == family_id). ID is caller-supplied, not left to the
// table's uuidv7() default, so it can double as a JWT's sid claim without
// a round trip to read it back.
type Row struct {
	ID          string
	UserID      string
	TenantID    string
	DeviceID    string
	RefreshHash string
	UserAgent   string
	IPAddress   string
	CountryCode string
	ExpiresAt   time.Time
}

// Insert creates a new session row. UserAgent, IPAddress, and CountryCode
// store as SQL NULL when empty, same convention as auditlog.Store.Write.
func (s *Store) Insert(ctx context.Context, row Row) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO system.sessions
			(id, user_id, tenant_id, family_id, device_id, refresh_hash, user_agent, ip_address, country_code, expires_at)
		VALUES ($1, $2, $3, $1, $4, $5, NULLIF($6, ''), NULLIF($7, '')::inet, NULLIF($8, ''), $9)
	`, row.ID, row.UserID, row.TenantID, row.DeviceID, row.RefreshHash, row.UserAgent, row.IPAddress, row.CountryCode, row.ExpiresAt)
	if err != nil {
		return fmt.Errorf("insert session row: %w", err)
	}

	return nil
}
