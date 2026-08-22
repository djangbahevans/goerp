// Package session bootstraps the system.sessions table, inserts a
// session's first row, and revokes rows — the table backing
// JWT/refresh-token issuance, rotation, and revocation (auth-internals.md
// §4 "Session table"). Rotation (successor rows on refresh) is a separate,
// unbuilt ticket; revocation only marks a row, it doesn't check any
// blocklist — that's internal/engine/sessionrevoke.
package session

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/db"
)

var ErrSessionNotFound = errors.New("session not found")

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

	// MFAMethod/MFAVerifiedAt/MFACredentialID are set only for a session
	// that completed MFA before this row is created (auth-internals.md §8
	// "Representing MFA assurance in sessions and tokens") — e.g. the
	// login flow's mfa_token branch (goerp#304) issuing the final session
	// after a successful factor verification. Left zero-value for an
	// ordinary password-only login, storing as SQL NULL the same way
	// UserAgent/IPAddress/CountryCode do.
	MFAMethod       string
	MFAVerifiedAt   *time.Time
	MFACredentialID string
}

// Insert creates a new session row. UserAgent, IPAddress, CountryCode, and
// the MFA fields store as SQL NULL when empty, same convention
// auditlog.Store.Write uses.
func (s *Store) Insert(ctx context.Context, row Row) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO system.sessions
			(id, user_id, tenant_id, family_id, device_id, refresh_hash, user_agent, ip_address, country_code, expires_at, mfa_verified_at, mfa_method, mfa_credential_id)
		VALUES ($1, $2, $3, $1, $4, $5, NULLIF($6, ''), NULLIF($7, '')::inet, NULLIF($8, ''), $9, $10, NULLIF($11, ''), NULLIF($12, '')::uuid)
	`, row.ID, row.UserID, row.TenantID, row.DeviceID, row.RefreshHash, row.UserAgent, row.IPAddress, row.CountryCode, row.ExpiresAt, row.MFAVerifiedAt, row.MFAMethod, row.MFACredentialID)
	if err != nil {
		return fmt.Errorf("insert session row: %w", err)
	}

	return nil
}

// Revoke sets id's revoked_at/revoke_reason. Idempotent: revoking an
// already-revoked row just refreshes revoked_at/revoke_reason rather than
// erroring — only a genuinely nonexistent id returns ErrSessionNotFound.
func (s *Store) Revoke(ctx context.Context, id, reason string) error {
	result, err := s.db.ExecContext(ctx, `
		UPDATE system.sessions SET revoked_at = NOW(), revoke_reason = $2 WHERE id = $1
	`, id, reason)
	if err != nil {
		return fmt.Errorf("revoke session: %w", err)
	}
	n, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("revoke session: %w", err)
	}
	if n == 0 {
		return ErrSessionNotFound
	}
	return nil
}

// UpdateMFAAssurance sets id's mfa_verified_at/mfa_method/mfa_credential_id
// columns — auth-internals.md §8 "Step-up re-verification" step 2,
// refreshing a session's MFA assurance in place without creating a new
// session row. Returns ErrSessionNotFound if id doesn't match any
// non-revoked row, same RowsAffected-checking convention Revoke uses.
func (s *Store) UpdateMFAAssurance(ctx context.Context, id, mfaMethod string, mfaVerifiedAt time.Time, mfaCredentialID string) error {
	result, err := s.db.ExecContext(ctx, `
		UPDATE system.sessions
		SET mfa_verified_at = $2, mfa_method = $3, mfa_credential_id = NULLIF($4, '')::uuid
		WHERE id = $1 AND revoked_at IS NULL
	`, id, mfaVerifiedAt, mfaMethod, mfaCredentialID)
	if err != nil {
		return fmt.Errorf("update session mfa assurance: %w", err)
	}
	n, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("update session mfa assurance: %w", err)
	}
	if n == 0 {
		return ErrSessionNotFound
	}
	return nil
}

// NonRevokedIDsForUser returns the ids of every session row for userID
// that isn't already revoked — the set RevokeAllForUser is about to
// revoke, needed by internal/engine/sessionrevoke.Revoker to also
// blocklist each one in Redis (a set the bulk UPDATE itself can't hand
// back after the fact).
func (s *Store) NonRevokedIDsForUser(ctx context.Context, userID string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id FROM system.sessions WHERE user_id = $1 AND revoked_at IS NULL
	`, userID)
	if err != nil {
		return nil, fmt.Errorf("query non-revoked sessions: %w", err)
	}
	defer func() { _ = rows.Close() }()

	ids := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan session id: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate session ids: %w", err)
	}

	return ids, nil
}

// RevokeAllForUser revokes every non-revoked session row for userID.
func (s *Store) RevokeAllForUser(ctx context.Context, userID, reason string) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE system.sessions SET revoked_at = NOW(), revoke_reason = $2
		WHERE user_id = $1 AND revoked_at IS NULL
	`, userID, reason)
	if err != nil {
		return fmt.Errorf("revoke all sessions for user: %w", err)
	}
	return nil
}

// NonRevokedIDsForUserInTenant returns the ids of every session row for
// userID within tenantID that isn't already revoked — the
// (user, tenant)-scoped counterpart to NonRevokedIDsForUser, needed the
// same way by internal/engine/sessionrevoke.Revoker.
// RevokeAllForUserInTenant. Distinct from plain NonRevokedIDsForUser:
// goerp#306's admin MFA reset must only revoke a target's sessions in the
// admin's own tenant, not every tenant that user happens to also belong
// to.
func (s *Store) NonRevokedIDsForUserInTenant(ctx context.Context, userID, tenantID string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id FROM system.sessions WHERE user_id = $1 AND tenant_id = $2 AND revoked_at IS NULL
	`, userID, tenantID)
	if err != nil {
		return nil, fmt.Errorf("query non-revoked sessions: %w", err)
	}
	defer func() { _ = rows.Close() }()

	ids := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan session id: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate session ids: %w", err)
	}

	return ids, nil
}

// RevokeAllForUserInTenant revokes every non-revoked session row for
// userID within tenantID — the (user, tenant)-scoped counterpart to
// RevokeAllForUser, same reasoning as NonRevokedIDsForUserInTenant.
func (s *Store) RevokeAllForUserInTenant(ctx context.Context, userID, tenantID, reason string) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE system.sessions SET revoked_at = NOW(), revoke_reason = $3
		WHERE user_id = $1 AND tenant_id = $2 AND revoked_at IS NULL
	`, userID, tenantID, reason)
	if err != nil {
		return fmt.Errorf("revoke all sessions for user in tenant: %w", err)
	}
	return nil
}

// NonRevokedIDsForTenant returns the ids of every session row for tenantID
// that isn't already revoked — the tenant-scoped counterpart to
// NonRevokedIDsForUser, needed the same way by
// internal/engine/sessionrevoke.Revoker.RevokeAllForTenant.
func (s *Store) NonRevokedIDsForTenant(ctx context.Context, tenantID string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id FROM system.sessions WHERE tenant_id = $1 AND revoked_at IS NULL
	`, tenantID)
	if err != nil {
		return nil, fmt.Errorf("query non-revoked sessions: %w", err)
	}
	defer func() { _ = rows.Close() }()

	ids := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan session id: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate session ids: %w", err)
	}

	return ids, nil
}

// RevokeAllForTenant revokes every non-revoked session row for tenantID —
// what a tenant suspend needs, distinct from RevokeAllForUser's per-user
// scope.
func (s *Store) RevokeAllForTenant(ctx context.Context, tenantID, reason string) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE system.sessions SET revoked_at = NOW(), revoke_reason = $2
		WHERE tenant_id = $1 AND revoked_at IS NULL
	`, tenantID, reason)
	if err != nil {
		return fmt.Errorf("revoke all sessions for tenant: %w", err)
	}
	return nil
}
