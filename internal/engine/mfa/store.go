package mfa

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/djangbahevans/goerp/internal/engine/db"
)

// createUserMFATable matches auth-internals.md §2's user_mfa schema
// exactly, schema-qualified against system (same convention tenant.Store's
// own DDL uses). sign_count isn't a column here — WebAuthn's sign count
// lives inside the encrypted Credential blob itself, updated by
// goerp#301's own logic re-writing that column, not tracked separately.
const createUserMFATable = `
CREATE TABLE IF NOT EXISTS system.user_mfa (
    id           UUID PRIMARY KEY DEFAULT uuidv7(),
    user_id      UUID NOT NULL REFERENCES system.users(id) ON DELETE CASCADE,
    type         TEXT NOT NULL CHECK (type IN ('totp', 'webauthn', 'recovery_code')),
    credential   BYTEA NOT NULL,
    label        TEXT,
    is_primary   BOOLEAN NOT NULL DEFAULT FALSE,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_used_at TIMESTAMPTZ,
    revoked_at   TIMESTAMPTZ
)
`

const createUserMFAUserIDIndex = `
CREATE INDEX IF NOT EXISTS idx_user_mfa_user_id ON system.user_mfa(user_id)
    WHERE revoked_at IS NULL
`

type Store struct {
	db *sql.DB
}

func NewStore(db *sql.DB) *Store {
	return &Store{db: db}
}

// Bootstrap creates system.user_mfa and its partial user_id index if they
// don't already exist. Idempotent and concurrent-safe against other
// processes calling Bootstrap at the same time, same convention
// tenant.Store.Bootstrap uses (goerp#171).
func (s *Store) Bootstrap(ctx context.Context) error {
	keys := []int64{db.SystemSchemaLockKey, db.AdvisoryLockKey("mfa.Bootstrap")}
	return db.WithAdvisoryLock(ctx, s.db, keys, func(tx *sql.Tx) error {
		if err := db.EnsureSystemSchema(ctx, tx); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, createUserMFATable); err != nil {
			return fmt.Errorf("create user_mfa table: %w", err)
		}
		if _, err := tx.ExecContext(ctx, createUserMFAUserIDIndex); err != nil {
			return fmt.Errorf("create user_mfa user_id index: %w", err)
		}
		return nil
	})
}

const userMFAColumns = `id, user_id, type, credential, label, is_primary, created_at, last_used_at, revoked_at`

type rowScanner interface {
	Scan(dest ...any) error
}

// scanCredential unpacks one userMFAColumns row.
func scanCredential(sc rowScanner) (*Credential, error) {
	var c Credential
	var label sql.NullString
	var lastUsedAt, revokedAt sql.NullTime

	if err := sc.Scan(
		&c.ID, &c.UserID, &c.Type, &c.Credential, &label, &c.IsPrimary,
		&c.CreatedAt, &lastUsedAt, &revokedAt,
	); err != nil {
		return nil, err
	}

	if label.Valid {
		c.Label = &label.String
	}
	if lastUsedAt.Valid {
		c.LastUsedAt = &lastUsedAt.Time
	}
	if revokedAt.Valid {
		c.RevokedAt = &revokedAt.Time
	}

	return &c, nil
}

// Insert creates a new MFA factor row for userID. credential must already
// be encrypted by the caller (goerp#297) — this store treats it as opaque
// bytes.
func (s *Store) Insert(ctx context.Context, userID string, credType CredentialType, credential []byte, label *string) (*Credential, error) {
	row := s.db.QueryRowContext(ctx, `
		INSERT INTO system.user_mfa (user_id, type, credential, label)
		VALUES ($1, $2, $3, $4)
		RETURNING `+userMFAColumns,
		userID, credType, credential, label,
	)
	c, err := scanCredential(row)
	if err != nil {
		return nil, fmt.Errorf("insert mfa credential: %w", err)
	}
	return c, nil
}

// ListActiveByUser returns userID's non-revoked MFA factors.
func (s *Store) ListActiveByUser(ctx context.Context, userID string) ([]*Credential, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT `+userMFAColumns+`
		FROM system.user_mfa
		WHERE user_id = $1 AND revoked_at IS NULL
		ORDER BY created_at
	`, userID)
	if err != nil {
		return nil, fmt.Errorf("list mfa credentials: %w", err)
	}
	defer rows.Close()

	var creds []*Credential
	for rows.Next() {
		c, err := scanCredential(rows)
		if err != nil {
			return nil, fmt.Errorf("list mfa credentials: %w", err)
		}
		creds = append(creds, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list mfa credentials: %w", err)
	}
	return creds, nil
}

// Revoke marks id revoked. Returns ErrCredentialNotFound if id doesn't
// match any row — same RowsAffected-checking convention
// session.Store.Revoke/apikey.Store.Revoke already use in this codebase,
// so a typo'd id doesn't silently report success. Revoking an
// already-revoked id still succeeds (re-sets revoked_at), matching those
// two Revoke methods' own idempotency — neither guards on the column's
// current value either.
func (s *Store) Revoke(ctx context.Context, id string) error {
	result, err := s.db.ExecContext(ctx, `
		UPDATE system.user_mfa SET revoked_at = NOW()
		WHERE id = $1
	`, id)
	if err != nil {
		return fmt.Errorf("revoke mfa credential: %w", err)
	}
	n, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("revoke mfa credential: %w", err)
	}
	if n == 0 {
		return ErrCredentialNotFound
	}
	return nil
}
