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
    mfa_credential_id UUID,
    persistent        BOOLEAN NOT NULL DEFAULT TRUE,
    password_change_required BOOLEAN NOT NULL DEFAULT FALSE
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
	// Persistent is false for a browser login without "remember this
	// device": the refresh cookie is session-scoped and the row gets the
	// shorter non-persistent TTL, carried forward across every rotation.
	Persistent bool

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

	// PasswordChangeRequired restricts the session until the password is
	// changed (auth-internals.md §3 "Password policy at sign-in"); every
	// rotation carries it forward.
	PasswordChangeRequired bool
}

// Insert creates a new session row. UserAgent, IPAddress, CountryCode, and
// the MFA fields store as SQL NULL when empty, same convention
// auditlog.Store.Write uses.
func (s *Store) Insert(ctx context.Context, row Row) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO system.sessions
			(id, user_id, tenant_id, family_id, device_id, refresh_hash, user_agent, ip_address, country_code, expires_at, mfa_verified_at, mfa_method, mfa_credential_id, persistent, password_change_required)
		VALUES ($1, $2, $3, $1, $4, $5, NULLIF($6, ''), NULLIF($7, '')::inet, NULLIF($8, ''), $9, $10, NULLIF($11, ''), NULLIF($12, '')::uuid, $13, $14)
	`, row.ID, row.UserID, row.TenantID, row.DeviceID, row.RefreshHash, row.UserAgent, row.IPAddress, row.CountryCode, row.ExpiresAt, row.MFAVerifiedAt, row.MFAMethod, row.MFACredentialID, row.Persistent, row.PasswordChangeRequired)
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

// ClearPasswordChangeRequired lifts the restriction from id's session
// family (auth-internals.md §3 "Password change" step 5) and returns id's
// ReissueState, or ErrSessionNotFound. Locking every live row of the family first waits out
// a Rotate in flight on any of them, and the UPDATE, a later statement,
// then also sees the row that Rotate inserted.
func (s *Store) ClearPasswordChangeRequired(ctx context.Context, id string) (ReissueState, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ReissueState{}, fmt.Errorf("begin clear password_change_required: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var familyID string
	var state ReissueState
	err = tx.QueryRowContext(ctx, `
		SELECT family_id, persistent, expires_at FROM system.sessions
		WHERE id = $1 AND revoked_at IS NULL
	`, id).Scan(&familyID, &state.Persistent, &state.ExpiresAt)
	if errors.Is(err, sql.ErrNoRows) {
		return ReissueState{}, ErrSessionNotFound
	}
	if err != nil {
		return ReissueState{}, fmt.Errorf("look up session: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		SELECT 1 FROM system.sessions WHERE family_id = $1 AND revoked_at IS NULL FOR UPDATE
	`, familyID); err != nil {
		return ReissueState{}, fmt.Errorf("lock session family: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE system.sessions SET password_change_required = FALSE
		WHERE family_id = $1 AND revoked_at IS NULL
	`, familyID); err != nil {
		return ReissueState{}, fmt.Errorf("clear session password_change_required: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return ReissueState{}, fmt.Errorf("commit clear password_change_required: %w", err)
	}
	return state, nil
}

// ReissueState is what an access token reissued for an existing session
// takes from its row.
type ReissueState struct {
	Persistent             bool
	ExpiresAt              time.Time
	PasswordChangeRequired bool
}

// UpdateMFAAssurance sets id's mfa_verified_at/mfa_method/mfa_credential_id
// columns — auth-internals.md §8 "Step-up re-verification" step 2,
// refreshing a session's MFA assurance in place without creating a new
// session row — and returns the row's ReissueState. Returns
// ErrSessionNotFound if id doesn't match any non-revoked row.
func (s *Store) UpdateMFAAssurance(ctx context.Context, id, mfaMethod string, mfaVerifiedAt time.Time, mfaCredentialID string) (ReissueState, error) {
	return updateMFAAssurance(ctx, s.db, id, mfaMethod, mfaVerifiedAt, mfaCredentialID)
}

// UpdateMFAAssuranceTx is UpdateMFAAssurance inside the caller's
// transaction.
func (s *Store) UpdateMFAAssuranceTx(ctx context.Context, tx *sql.Tx, id, mfaMethod string, mfaVerifiedAt time.Time, mfaCredentialID string) (ReissueState, error) {
	return updateMFAAssurance(ctx, tx, id, mfaMethod, mfaVerifiedAt, mfaCredentialID)
}

func updateMFAAssurance(ctx context.Context, q db.Execer, id, mfaMethod string, mfaVerifiedAt time.Time, mfaCredentialID string) (ReissueState, error) {
	var state ReissueState
	err := q.QueryRowContext(ctx, `
		UPDATE system.sessions
		SET mfa_verified_at = $2, mfa_method = $3, mfa_credential_id = NULLIF($4, '')::uuid
		WHERE id = $1 AND revoked_at IS NULL
		RETURNING persistent, expires_at, password_change_required
	`, id, mfaVerifiedAt, mfaMethod, mfaCredentialID).Scan(&state.Persistent, &state.ExpiresAt, &state.PasswordChangeRequired)
	if errors.Is(err, sql.ErrNoRows) {
		return ReissueState{}, ErrSessionNotFound
	}
	if err != nil {
		return ReissueState{}, fmt.Errorf("update session mfa assurance: %w", err)
	}
	return state, nil
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

// RevokeAllForUserTx revokes every non-revoked session row for userID
// inside the caller's transaction and returns the revoked ids.
func (s *Store) RevokeAllForUserTx(ctx context.Context, tx *sql.Tx, userID, reason string) ([]string, error) {
	rows, err := revokeUntilSettled(ctx, tx, `
		UPDATE system.sessions SET revoked_at = NOW(), revoke_reason = $2
		WHERE user_id = $1 AND revoked_at IS NULL
		RETURNING `+revokedColumns, userID, reason)
	if err != nil {
		return nil, fmt.Errorf("revoke all sessions for user: %w", err)
	}
	return rowIDs(rows), nil
}

// RevokeOthersForUser revokes every non-revoked session row for userID
// outside keepSessionID's family (so the caller's own rotation chain
// survives) and returns the revoked ids.
func (s *Store) RevokeOthersForUser(ctx context.Context, userID, keepSessionID, reason string) ([]string, error) {
	rows, err := s.revokeUntilSettled(ctx, `
		UPDATE system.sessions SET revoked_at = NOW(), revoke_reason = $3
		WHERE user_id = $1 AND revoked_at IS NULL
		  AND family_id IS DISTINCT FROM (SELECT family_id FROM system.sessions WHERE id = $2)
		RETURNING `+revokedColumns, userID, keepSessionID, reason)
	if err != nil {
		return nil, fmt.Errorf("revoke other sessions for user: %w", err)
	}
	return rowIDs(rows), nil
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
