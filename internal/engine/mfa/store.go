package mfa

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/djangbahevans/goerp/internal/engine/db"
)

const createUserMFATable = `
CREATE TABLE IF NOT EXISTS system.user_mfa (
    id           UUID PRIMARY KEY DEFAULT uuidv7(),
    user_id      UUID NOT NULL REFERENCES system.users(id) ON DELETE CASCADE,
    tenant_id    UUID REFERENCES system.tenants(id) ON DELETE CASCADE,
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

const userMFAColumns = `id, user_id, tenant_id, type, credential, label, is_primary, created_at, last_used_at, revoked_at`

type rowScanner interface {
	Scan(dest ...any) error
}

func scanCredential(sc rowScanner) (*Credential, error) {
	var c Credential
	var label sql.NullString
	var lastUsedAt, revokedAt sql.NullTime

	if err := sc.Scan(
		&c.ID, &c.UserID, &c.TenantID, &c.Type, &c.Credential, &label, &c.IsPrimary,
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

func (s *Store) Insert(ctx context.Context, userID string, credType CredentialType, credential []byte, label *string) (*Credential, error) {
	return insert(ctx, s.db, userID, nil, credType, credential, label)
}

func (s *Store) InsertTx(ctx context.Context, tx *sql.Tx, userID string, credType CredentialType, credential []byte, label *string) (*Credential, error) {
	return insert(ctx, tx, userID, nil, credType, credential, label)
}

func (s *Store) InsertScopedTx(ctx context.Context, tx *sql.Tx, userID string, tenantID *string, credType CredentialType, credential []byte, label *string) (*Credential, error) {
	return insert(ctx, tx, userID, tenantID, credType, credential, label)
}

func insert(ctx context.Context, q db.Execer, userID string, tenantID *string, credType CredentialType, credential []byte, label *string) (*Credential, error) {
	row := q.QueryRowContext(ctx, `
		INSERT INTO system.user_mfa (user_id, tenant_id, type, credential, label)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING `+userMFAColumns,
		userID, tenantID, credType, credential, label,
	)
	c, err := scanCredential(row)
	if err != nil {
		return nil, fmt.Errorf("insert mfa credential: %w", err)
	}

	return c, nil
}

func (s *Store) WithTx(ctx context.Context, fn func(tx *sql.Tx) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin mfa transaction: %w", err)
	}

	defer func() {
		_ = tx.Rollback()
	}()
	if err := fn(tx); err != nil {
		return err
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit mfa transaction: %w", err)
	}

	return nil
}

// LockUserTx serializes MFA changes without blocking session inserts' foreign-key
// locks. A key-changing lock would deadlock with rotation holding a session row
// while MFA revocation waits for that row.
func (s *Store) LockUserTx(ctx context.Context, tx *sql.Tx, userID string) error {
	var id string
	err := tx.QueryRowContext(ctx, `SELECT id FROM system.users WHERE id = $1 FOR NO KEY UPDATE`, userID).Scan(&id)
	if err != nil {
		return fmt.Errorf("lock user for mfa change: %w", err)
	}

	return nil
}

func (s *Store) HasActiveOfTypeTx(ctx context.Context, tx *sql.Tx, userID string, credType CredentialType) (bool, error) {
	var exists bool
	err := tx.QueryRowContext(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM system.user_mfa
			WHERE user_id = $1 AND type = $2 AND revoked_at IS NULL
		)
	`, userID, credType).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("check active mfa credentials: %w", err)
	}

	return exists, nil
}

func (s *Store) ListActiveByUser(ctx context.Context, userID string) ([]*Credential, error) {
	return listActiveByUser(ctx, s.db, userID)
}

func (s *Store) ListActiveByUserTx(ctx context.Context, tx *sql.Tx, userID string) ([]*Credential, error) {
	return listActiveByUser(ctx, tx, userID)
}

type querier interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

func listActiveByUser(ctx context.Context, q querier, userID string) ([]*Credential, error) {
	rows, err := q.QueryContext(ctx, `
		SELECT `+userMFAColumns+`
		FROM system.user_mfa
		WHERE user_id = $1 AND revoked_at IS NULL
		ORDER BY created_at
	`, userID)
	if err != nil {
		return nil, fmt.Errorf("list mfa credentials: %w", err)
	}

	defer func() {
		_ = rows.Close()
	}()

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

// Revoke is idempotent; single-use proofs require ConsumeOnce instead.
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

func (s *Store) RevokeTx(ctx context.Context, tx *sql.Tx, userID, id string) error {
	result, err := tx.ExecContext(ctx, `
		UPDATE system.user_mfa SET revoked_at = NOW()
		WHERE id = $1 AND user_id = $2 AND revoked_at IS NULL
	`, id, userID)
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

func (s *Store) RevokeAllOfTypeTx(ctx context.Context, tx *sql.Tx, userID string, credType CredentialType) error {
	_, err := tx.ExecContext(ctx, `
		UPDATE system.user_mfa SET revoked_at = NOW()
		WHERE user_id = $1 AND type = $2 AND revoked_at IS NULL
	`, userID, credType)
	if err != nil {
		return fmt.Errorf("revoke mfa credentials of type %s: %w", credType, err)
	}

	return nil
}

func (s *Store) TouchLastUsed(ctx context.Context, id string) error {
	if _, err := s.db.ExecContext(ctx, `UPDATE system.user_mfa SET last_used_at = NOW() WHERE id = $1`, id); err != nil {
		return fmt.Errorf("touch mfa credential: %w", err)
	}

	return nil
}

// UpdateCredentialAfterUse preserves the encrypted WebAuthn payload while updating its sign count and last use.
func (s *Store) UpdateCredentialAfterUse(ctx context.Context, id string, credential []byte) error {
	result, err := s.db.ExecContext(ctx, `
		UPDATE system.user_mfa SET credential = $2, last_used_at = NOW()
		WHERE id = $1 AND revoked_at IS NULL
	`, id, credential)
	if err != nil {
		return fmt.Errorf("update mfa credential: %w", err)
	}

	n, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("update mfa credential: %w", err)
	}

	if n == 0 {
		return ErrCredentialNotFound
	}

	return nil
}

func (s *Store) RevokeAllForUser(ctx context.Context, userID string) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE system.user_mfa SET revoked_at = NOW()
		WHERE user_id = $1 AND revoked_at IS NULL
	`, userID)
	if err != nil {
		return fmt.Errorf("revoke all mfa credentials for user: %w", err)
	}

	return nil
}

// ConsumeOnce rejects an already revoked row, preventing concurrent reuse of a recovery code.
func (s *Store) ConsumeOnce(ctx context.Context, id string) error {
	return consumeOnce(ctx, s.db, id)
}

func (s *Store) ConsumeOnceTx(ctx context.Context, tx *sql.Tx, id string) error {
	return consumeOnce(ctx, tx, id)
}

func consumeOnce(ctx context.Context, q db.Execer, id string) error {
	result, err := q.ExecContext(ctx, `
		UPDATE system.user_mfa SET revoked_at = NOW()
		WHERE id = $1 AND revoked_at IS NULL
	`, id)
	if err != nil {
		return fmt.Errorf("consume mfa credential: %w", err)
	}

	n, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("consume mfa credential: %w", err)
	}

	if n == 0 {
		return ErrCredentialNotFound
	}

	return nil
}

func (s *Store) GetByID(ctx context.Context, userID, id string) (*Credential, error) {
	c, err := scanCredential(s.db.QueryRowContext(ctx, `SELECT `+userMFAColumns+` FROM system.user_mfa WHERE user_id = $1 AND id = $2`, userID, id))
	if err != nil {
		return nil, fmt.Errorf("get mfa credential: %w", err)
	}

	return c, nil
}
