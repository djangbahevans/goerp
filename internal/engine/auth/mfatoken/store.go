// Package mfatoken issues and verifies the mfa_token — auth-internals.md
// §8 "MFA token flow"'s short-lived (5-minute), HMAC-signed token that
// binds a single login attempt awaiting MFA verification to its user,
// tenant, and origin. Key loading follows the same Active/Previous shape
// as signingkey's JWT keys and rowcrypt's row-encryption keys (see
// internal/engine/auth/signingkey, internal/engine/auth/rowcrypt), with
// key material held in the secrets.Backend rather than generated fresh
// per process — every engine replica must be able to verify a token any
// replica issued. 90-day rotation is out of scope here for the same
// reason it's out of scope for signingkey: it requires the engine's
// hot-reload mechanism (backlog #168, unfiled) to coordinate across
// replicas; nothing in this package rotates a key or produces a Previous
// entry on its own.
package mfatoken

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/djangbahevans/goerp/internal/engine/db"
	"github.com/djangbahevans/goerp/internal/engine/secrets"
)

const createMFATokenSigningKeysTable = `
CREATE TABLE IF NOT EXISTS system.mfa_token_signing_keys (
    key_id                  UUID PRIMARY KEY DEFAULT uuidv7(),
    is_active               BOOLEAN NOT NULL DEFAULT TRUE,
    created_at              TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    secret_manager_version  TEXT NOT NULL
)
`

const createMFATokenSigningKeysActiveIndex = `
CREATE UNIQUE INDEX IF NOT EXISTS mfa_token_signing_keys_active_unique_idx
    ON system.mfa_token_signing_keys (is_active) WHERE is_active = true
`

type Store struct {
	db      *sql.DB
	secrets secrets.Backend
}

func NewStore(db *sql.DB, secretsBackend secrets.Backend) *Store {
	return &Store{db: db, secrets: secretsBackend}
}

// Bootstrap creates system.mfa_token_signing_keys and its active-key
// partial unique index if they don't already exist. Idempotent and
// concurrent-safe against other processes calling Bootstrap at the same
// time, same convention signingkey.Store.Bootstrap and
// rowcrypt.Store.Bootstrap use.
func (s *Store) Bootstrap(ctx context.Context) error {
	keys := []int64{db.SystemSchemaLockKey, db.AdvisoryLockKey("mfatoken.Bootstrap")}
	return db.WithAdvisoryLock(ctx, s.db, keys, func(tx *sql.Tx) error {
		if err := db.EnsureSystemSchema(ctx, tx); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, createMFATokenSigningKeysTable); err != nil {
			return fmt.Errorf("create mfa_token_signing_keys table: %w", err)
		}
		if _, err := tx.ExecContext(ctx, createMFATokenSigningKeysActiveIndex); err != nil {
			return fmt.Errorf("create mfa_token_signing_keys active index: %w", err)
		}
		return nil
	})
}

// Key is one HMAC-SHA256 key the engine can sign or verify mfa_tokens
// with.
type Key struct {
	KeyID                string
	Secret               []byte // 32 bytes
	CreatedAt            time.Time
	SecretManagerVersion string
}

// KeySet is the engine's in-memory view of its mfa_token signing keys.
// Previous stays empty until something outside this package (a future
// rotation job) marks a key inactive — LoadOrGenerate only ever generates
// the first Active key on an empty table.
type KeySet struct {
	Active   Key
	Previous []Key
}

func secretName(keyID string) string {
	return "GOERP_MFA_TOKEN_SIGNING_KEY_" + keyID
}

// LoadOrGenerate returns the engine's mfa_token signing key set,
// generating and persisting a new HMAC-SHA256 key on first boot if none
// exists yet. Concurrent-safe against other engine replicas racing to
// bootstrap the same key via db.WithAdvisoryLock — only one generates;
// the rest load whatever the winner wrote.
func (s *Store) LoadOrGenerate(ctx context.Context) (*KeySet, error) {
	var set KeySet
	lockKeys := []int64{db.AdvisoryLockKey("mfatoken.LoadOrGenerate")}
	err := db.WithAdvisoryLock(ctx, s.db, lockKeys, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `
			SELECT key_id, is_active, created_at, secret_manager_version
			FROM system.mfa_token_signing_keys
			ORDER BY created_at
		`)
		if err != nil {
			return fmt.Errorf("query mfa token signing keys: %w", err)
		}
		defer func() { _ = rows.Close() }()

		type row struct {
			keyID, secretManagerVersion string
			isActive                    bool
			createdAt                   time.Time
		}
		var found []row
		for rows.Next() {
			var r row
			if err := rows.Scan(&r.keyID, &r.isActive, &r.createdAt, &r.secretManagerVersion); err != nil {
				return fmt.Errorf("scan mfa token signing key: %w", err)
			}
			found = append(found, r)
		}
		if err := rows.Err(); err != nil {
			return fmt.Errorf("query mfa token signing keys: %w", err)
		}

		if len(found) == 0 {
			generated, err := generateAndStore(ctx, tx, s.secrets)
			if err != nil {
				return err
			}
			set = KeySet{Active: *generated}
			return nil
		}

		for _, r := range found {
			material, err := s.secrets.Get(ctx, secretName(r.keyID))
			if err != nil {
				return fmt.Errorf("load key material for key id %s: %w", r.keyID, err)
			}
			secret, err := base64.StdEncoding.DecodeString(material)
			if err != nil {
				return fmt.Errorf("decode key material for key id %s: %w", r.keyID, err)
			}
			k := Key{
				KeyID:                r.keyID,
				Secret:               secret,
				CreatedAt:            r.createdAt,
				SecretManagerVersion: r.secretManagerVersion,
			}
			if r.isActive {
				set.Active = k
			} else {
				set.Previous = append(set.Previous, k)
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &set, nil
}

// generateAndStore creates a new random 32-byte HMAC-SHA256 key, writes it
// to secretsBackend, and records its metadata in
// system.mfa_token_signing_keys. If secretsBackend doesn't support Set
// (secrets.ErrSetNotSupported — true of the "env" backend, dev-only per
// its own package doc), the key isn't persisted anywhere: it's returned
// for this process to use, but a restart or another replica won't see it
// and will generate its own — the same accepted dev-mode tradeoff
// signingkey.generateAndStore and rowcrypt.generateAndStore document, for
// the same reason (a mfa_token_signing_keys row without its key material
// recoverable would silently break every future restart's verify path).
func generateAndStore(ctx context.Context, tx *sql.Tx, secretsBackend secrets.Backend) (*Key, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return nil, fmt.Errorf("generate HMAC-SHA256 key: %w", err)
	}

	now := time.Now()
	key := &Key{
		Secret:    raw,
		CreatedAt: now,
	}

	keyID := uuid.NewString()
	material := base64.StdEncoding.EncodeToString(raw)
	if setErr := secretsBackend.Set(ctx, secretName(keyID), material); setErr != nil {
		if errors.Is(setErr, secrets.ErrSetNotSupported) {
			key.KeyID = keyID
			key.SecretManagerVersion = "ephemeral"
			return key, nil
		}
		return nil, fmt.Errorf("store key material: %w", setErr)
	}

	const secretManagerVersion = "1"
	row := tx.QueryRowContext(ctx, `
		INSERT INTO system.mfa_token_signing_keys (key_id, is_active, created_at, secret_manager_version)
		VALUES ($1, true, $2, $3)
		RETURNING key_id
	`, keyID, now, secretManagerVersion)
	if err := row.Scan(&key.KeyID); err != nil {
		return nil, fmt.Errorf("insert mfa_token_signing_keys row: %w", err)
	}
	key.SecretManagerVersion = secretManagerVersion

	return key, nil
}
