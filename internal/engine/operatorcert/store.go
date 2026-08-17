// Package operatorcert tracks which serial number the admin gateway's
// operator mTLS certificates were issued under, per operator name — Vault
// PKI revokes by serial (internal/engine/vaultpki), but goerp admin
// operators revoke-cert (cli-reference.md §10) takes a name, so this is
// the name -> serial lookup that makes that possible. Never stores
// certificate or private key material, only issuance metadata.
package operatorcert

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/db"
)

const createOperatorCertificatesTable = `
CREATE TABLE IF NOT EXISTS system.operator_certificates (
    id             UUID PRIMARY KEY DEFAULT uuidv7(),
    name           TEXT NOT NULL,
    serial_number  TEXT NOT NULL,
    issued_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    expires_at     TIMESTAMPTZ NOT NULL,
    revoked_at     TIMESTAMPTZ
)
`

const createOperatorCertificatesNameIndex = `
CREATE INDEX IF NOT EXISTS idx_operator_certificates_name ON system.operator_certificates(name, issued_at DESC)
`

var ErrCertificateNotFound = errors.New("no live certificate found for that name")

type Store struct {
	db *sql.DB
}

func NewStore(db *sql.DB) *Store {
	return &Store{db: db}
}

// Bootstrap creates system.operator_certificates (and its index) if it
// doesn't already exist. Idempotent, concurrent-safe against other
// processes calling Bootstrap at the same time (goerp#171), same pattern
// as auditlog.Store.Bootstrap.
func (s *Store) Bootstrap(ctx context.Context) error {
	keys := []int64{db.SystemSchemaLockKey, db.AdvisoryLockKey("operatorcert.Bootstrap")}
	return db.WithAdvisoryLock(ctx, s.db, keys, func(tx *sql.Tx) error {
		if err := db.EnsureSystemSchema(ctx, tx); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, createOperatorCertificatesTable); err != nil {
			return fmt.Errorf("create operator_certificates table: %w", err)
		}
		if _, err := tx.ExecContext(ctx, createOperatorCertificatesNameIndex); err != nil {
			return fmt.Errorf("create operator_certificates name index: %w", err)
		}
		return nil
	})
}

// RecordIssuance records that name was issued a certificate with the
// given serial, expiring at expiresAt. Never stores certificate or key
// material — the admin API returns those to the caller once and keeps
// nothing else (goerp#181's own AC).
func (s *Store) RecordIssuance(ctx context.Context, name, serial string, expiresAt time.Time) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO system.operator_certificates (name, serial_number, expires_at)
		VALUES ($1, $2, $3)
	`, name, serial, expiresAt)
	if err != nil {
		return fmt.Errorf("record certificate issuance: %w", err)
	}
	return nil
}

// SerialForName returns the most recently issued, not-yet-revoked
// certificate's serial number for name. Returns ErrCertificateNotFound if
// none exists.
func (s *Store) SerialForName(ctx context.Context, name string) (string, error) {
	var serial string
	err := s.db.QueryRowContext(ctx, `
		SELECT serial_number FROM system.operator_certificates
		WHERE name = $1 AND revoked_at IS NULL
		ORDER BY issued_at DESC
		LIMIT 1
	`, name).Scan(&serial)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", ErrCertificateNotFound
		}
		return "", fmt.Errorf("find certificate for %q: %w", name, err)
	}
	return serial, nil
}

// MarkRevoked marks every live (not-yet-revoked) row for name as revoked
// — plural since re-issuing without revoking the previous certificate can
// leave more than one live row, and cutting off an identity means all of
// them.
func (s *Store) MarkRevoked(ctx context.Context, name string) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE system.operator_certificates
		SET revoked_at = NOW()
		WHERE name = $1 AND revoked_at IS NULL
	`, name)
	if err != nil {
		return fmt.Errorf("mark certificates revoked for %q: %w", name, err)
	}
	return nil
}
