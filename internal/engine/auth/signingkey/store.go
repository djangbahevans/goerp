// Package signingkey loads or generates the engine's RSA-2048 JWT signing key.
package signingkey

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"database/sql"
	"encoding/pem"
	"errors"
	"fmt"
	"time"
	"uuid"

	"github.com/djangbahevans/goerp/internal/engine/db"
	"github.com/djangbahevans/goerp/internal/engine/secrets"
)

const createJWTSigningKeysTable = `
CREATE TABLE IF NOT EXISTS system.jwt_signing_keys (
    kid                    UUID PRIMARY KEY DEFAULT uuidv7(),
    algorithm              TEXT NOT NULL,
    public_key             TEXT NOT NULL,
    is_active              BOOLEAN NOT NULL DEFAULT TRUE,
    created_at              TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    expires_at              TIMESTAMPTZ NOT NULL,
    secret_manager_version  TEXT NOT NULL
)
`

const createJWTSigningKeysActiveIndex = `
CREATE UNIQUE INDEX IF NOT EXISTS jwt_signing_keys_active_unique_idx
    ON system.jwt_signing_keys (is_active) WHERE is_active = true
`

// Generated active keys expire after 90 days; this package records expiry without rotating
// them.
const keyLifetime = 90 * 24 * time.Hour

type Store struct {
	db      *sql.DB
	secrets secrets.Backend
}

func NewStore(db *sql.DB, secretsBackend secrets.Backend) *Store {
	return &Store{db: db, secrets: secretsBackend}
}

// Bootstrap creates system.jwt_signing_keys and its active-key partial
// unique index if they don't already exist. Idempotent — safe to call on
// every engine startup, same as auditlog.Store.Bootstrap. Concurrent-safe
// against other processes calling Bootstrap at the same time via
// db.WithAdvisoryLock.
func (s *Store) Bootstrap(ctx context.Context) error {
	keys := []int64{db.SystemSchemaLockKey, db.AdvisoryLockKey("signingkey.Bootstrap")}
	return db.WithAdvisoryLock(ctx, s.db, keys, func(tx *sql.Tx) error {
		if err := db.EnsureSystemSchema(ctx, tx); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, createJWTSigningKeysTable); err != nil {
			return fmt.Errorf("create jwt_signing_keys table: %w", err)
		}
		if _, err := tx.ExecContext(ctx, createJWTSigningKeysActiveIndex); err != nil {
			return fmt.Errorf("create jwt_signing_keys active index: %w", err)
		}
		return nil
	})
}

type SigningKey struct {
	KID                  string
	Algorithm            string
	Private              *rsa.PrivateKey
	Public               *rsa.PublicKey
	CreatedAt            time.Time
	ExpiresAt            time.Time
	SecretManagerVersion string
}

// SigningKeySet holds active and previous signing keys. LoadOrGenerate initializes the
// active key but does not rotate keys.
type SigningKeySet struct {
	Active   SigningKey
	Previous []SigningKey
}

func secretName(kid string) string {
	return "GOERP_JWT_SIGNING_KEY_" + kid
}

// LoadOrGenerate returns the engine's Active signing key, generating and
// persisting a new RSA-2048 key pair on first boot if none exists yet.
// Concurrent-safe against other engine replicas racing to bootstrap the
// same key via db.WithAdvisoryLock — only one generates; the rest load
// whatever the winner wrote.
func (s *Store) LoadOrGenerate(ctx context.Context) (*SigningKeySet, error) {
	var key SigningKey
	lockKeys := []int64{db.AdvisoryLockKey("signingkey.LoadOrGenerate")}
	err := db.WithAdvisoryLock(ctx, s.db, lockKeys, func(tx *sql.Tx) error {
		row := tx.QueryRowContext(ctx, `
			SELECT kid, algorithm, created_at, expires_at, secret_manager_version
			FROM system.jwt_signing_keys WHERE is_active = true
		`)
		var kid, algorithm, secretManagerVersion string
		var createdAt, expiresAt time.Time
		switch err := row.Scan(&kid, &algorithm, &createdAt, &expiresAt, &secretManagerVersion); {
		case errors.Is(err, sql.ErrNoRows):
			generated, err := generateAndStore(ctx, tx, s.secrets)
			if err != nil {
				return err
			}
			key = *generated
			return nil
		case err != nil:
			return fmt.Errorf("query active signing key: %w", err)
		}

		privatePEM, err := s.secrets.Get(ctx, secretName(kid))
		if err != nil {
			return fmt.Errorf("load private key material for kid %s: %w", kid, err)
		}
		private, err := parsePrivateKeyPEM(privatePEM)
		if err != nil {
			return fmt.Errorf("parse private key material for kid %s: %w", kid, err)
		}

		key = SigningKey{
			KID:                  kid,
			Algorithm:            algorithm,
			Private:              private,
			Public:               &private.PublicKey,
			CreatedAt:            createdAt,
			ExpiresAt:            expiresAt,
			SecretManagerVersion: secretManagerVersion,
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &SigningKeySet{Active: key}, nil
}

// generateAndStore persists private key material before public metadata. EnvBackend uses
// ephemeral process-local keys; other persistence failures abort generation to avoid
// unreadable key records.
func generateAndStore(ctx context.Context, tx *sql.Tx, secretsBackend secrets.Backend) (*SigningKey, error) {
	private, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, fmt.Errorf("generate RSA-2048 key pair: %w", err)
	}

	now := time.Now()
	key := &SigningKey{
		Algorithm: "RS256",
		Private:   private,
		Public:    &private.PublicKey,
		CreatedAt: now,
		ExpiresAt: now.Add(keyLifetime),
	}

	privatePEM, err := encodePrivateKeyPEM(private)
	if err != nil {
		return nil, fmt.Errorf("encode private key: %w", err)
	}
	publicPEM, err := encodePublicKeyPEM(&private.PublicKey)
	if err != nil {
		return nil, fmt.Errorf("encode public key: %w", err)
	}

	ephemeralKID := uuid.New().String()
	if setErr := secretsBackend.Set(ctx, secretName(ephemeralKID), privatePEM); setErr != nil {
		if errors.Is(setErr, secrets.ErrSetNotSupported) {
			key.KID = ephemeralKID
			key.SecretManagerVersion = "ephemeral"
			return key, nil
		}
		return nil, fmt.Errorf("store private key material: %w", setErr)
	}

	const secretManagerVersion = "1"
	row := tx.QueryRowContext(ctx, `
		INSERT INTO system.jwt_signing_keys (kid, algorithm, public_key, is_active, created_at, expires_at, secret_manager_version)
		VALUES ($1, $2, $3, true, $4, $5, $6)
		RETURNING kid
	`, ephemeralKID, key.Algorithm, publicPEM, key.CreatedAt, key.ExpiresAt, secretManagerVersion)
	if err := row.Scan(&key.KID); err != nil {
		return nil, fmt.Errorf("insert jwt_signing_keys row: %w", err)
	}
	key.SecretManagerVersion = secretManagerVersion

	return key, nil
}

func encodePrivateKeyPEM(key *rsa.PrivateKey) (string, error) {
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return "", err
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})), nil
}

func encodePublicKeyPEM(key *rsa.PublicKey) (string, error) {
	der, err := x509.MarshalPKIXPublicKey(key)
	if err != nil {
		return "", err
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der})), nil
}

func parsePrivateKeyPEM(s string) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode([]byte(s))
	if block == nil {
		return nil, errors.New("no PEM block found")
	}
	key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, err
	}
	rsaKey, ok := key.(*rsa.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("expected *rsa.PrivateKey, got %T", key)
	}
	return rsaKey, nil
}
