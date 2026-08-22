// Package rowcrypt implements AES-256-GCM encryption for sensitive
// database columns (auth-internals.md §2 "Sensitive field encryption") —
// user_mfa.credential today; user_identities' OAuth/SAML token columns are
// a later caller once that table exists. Key loading follows the same
// Active/Previous shape as signingkey's JWT keys (see
// internal/engine/auth/signingkey), with key material held in the
// secrets.Backend rather than a asymmetric key pair. The background
// re-encryption job that empties Previous once no row references a key
// anymore, and the emergency key-compromise procedure, are separate,
// larger scope for a later pass — nothing in this package rotates a key
// or produces a Previous entry on its own.
package rowcrypt

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/db"
	"github.com/djangbahevans/goerp/internal/engine/secrets"
	"github.com/google/uuid"
)

var (
	ErrMalformedCiphertext = errors.New("malformed ciphertext")
	ErrUnknownKeyID        = errors.New("ciphertext references an unknown key id")
)

const createRowEncryptionKeysTable = `
CREATE TABLE IF NOT EXISTS system.row_encryption_keys (
    key_id                 UUID PRIMARY KEY DEFAULT uuidv7(),
    is_active               BOOLEAN NOT NULL DEFAULT TRUE,
    created_at              TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    secret_manager_version  TEXT NOT NULL
)
`

const createRowEncryptionKeysActiveIndex = `
CREATE UNIQUE INDEX IF NOT EXISTS row_encryption_keys_active_unique_idx
    ON system.row_encryption_keys (is_active) WHERE is_active = true
`

type Store struct {
	db      *sql.DB
	secrets secrets.Backend
}

func NewStore(db *sql.DB, secretsBackend secrets.Backend) *Store {
	return &Store{db: db, secrets: secretsBackend}
}

// Bootstrap creates system.row_encryption_keys and its active-key partial
// unique index if they don't already exist. Idempotent and concurrent-safe
// against other processes calling Bootstrap at the same time, same
// convention signingkey.Store.Bootstrap uses.
func (s *Store) Bootstrap(ctx context.Context) error {
	keys := []int64{db.SystemSchemaLockKey, db.AdvisoryLockKey("rowcrypt.Bootstrap")}
	return db.WithAdvisoryLock(ctx, s.db, keys, func(tx *sql.Tx) error {
		if err := db.EnsureSystemSchema(ctx, tx); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, createRowEncryptionKeysTable); err != nil {
			return fmt.Errorf("create row_encryption_keys table: %w", err)
		}
		if _, err := tx.ExecContext(ctx, createRowEncryptionKeysActiveIndex); err != nil {
			return fmt.Errorf("create row_encryption_keys active index: %w", err)
		}
		return nil
	})
}

// RowKey is one AES-256 key the engine can encrypt or decrypt row values
// with.
type RowKey struct {
	KeyID                string
	Key                  []byte // 32 bytes
	CreatedAt            time.Time
	SecretManagerVersion string
}

// RowKeySet is the engine's in-memory view of its row-encryption keys.
// Previous stays empty until something outside this package (a future
// rotation job) marks a key inactive — LoadOrGenerate only ever generates
// the first Active key on an empty table.
type RowKeySet struct {
	Active   RowKey
	Previous []RowKey
}

func secretName(keyID string) string {
	return "GOERP_ROW_ENCRYPTION_KEY_" + keyID
}

// LoadOrGenerate returns the engine's row-encryption key set, generating
// and persisting a new AES-256 key on first boot if none exists yet.
// Concurrent-safe against other engine replicas racing to bootstrap the
// same key via db.WithAdvisoryLock — only one generates; the rest load
// whatever the winner wrote.
func (s *Store) LoadOrGenerate(ctx context.Context) (*RowKeySet, error) {
	var set RowKeySet
	lockKeys := []int64{db.AdvisoryLockKey("rowcrypt.LoadOrGenerate")}
	err := db.WithAdvisoryLock(ctx, s.db, lockKeys, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `
			SELECT key_id, is_active, created_at, secret_manager_version
			FROM system.row_encryption_keys
			ORDER BY created_at
		`)
		if err != nil {
			return fmt.Errorf("query row encryption keys: %w", err)
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
				return fmt.Errorf("scan row encryption key: %w", err)
			}
			found = append(found, r)
		}
		if err := rows.Err(); err != nil {
			return fmt.Errorf("query row encryption keys: %w", err)
		}

		if len(found) == 0 {
			generated, err := generateAndStore(ctx, tx, s.secrets)
			if err != nil {
				return err
			}
			set = RowKeySet{Active: *generated}
			return nil
		}

		for _, r := range found {
			material, err := s.secrets.Get(ctx, secretName(r.keyID))
			if err != nil {
				return fmt.Errorf("load key material for key id %s: %w", r.keyID, err)
			}
			key, err := base64.StdEncoding.DecodeString(material)
			if err != nil {
				return fmt.Errorf("decode key material for key id %s: %w", r.keyID, err)
			}
			rk := RowKey{
				KeyID:                r.keyID,
				Key:                  key,
				CreatedAt:            r.createdAt,
				SecretManagerVersion: r.secretManagerVersion,
			}
			if r.isActive {
				set.Active = rk
			} else {
				set.Previous = append(set.Previous, rk)
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &set, nil
}

// generateAndStore creates a new random AES-256 key, writes it to
// secretsBackend, and records its metadata in system.row_encryption_keys.
// If secretsBackend doesn't support Set (secrets.ErrSetNotSupported — true
// of the "env" backend, dev-only per its own package doc), the key isn't
// persisted anywhere: it's returned for this process to use, but a
// restart or another replica won't see it and will generate its own —
// the same accepted dev-mode tradeoff signingkey.generateAndStore
// documents, for the same reason (a row_encryption_keys row without its
// key material recoverable would silently break every future restart's
// decrypt path).
func generateAndStore(ctx context.Context, tx *sql.Tx, secretsBackend secrets.Backend) (*RowKey, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return nil, fmt.Errorf("generate AES-256 key: %w", err)
	}

	now := time.Now()
	key := &RowKey{
		Key:       raw,
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
		INSERT INTO system.row_encryption_keys (key_id, is_active, created_at, secret_manager_version)
		VALUES ($1, true, $2, $3)
		RETURNING key_id
	`, keyID, now, secretManagerVersion)
	if err := row.Scan(&key.KeyID); err != nil {
		return nil, fmt.Errorf("insert row_encryption_keys row: %w", err)
	}
	key.SecretManagerVersion = secretManagerVersion

	return key, nil
}

// Encrypt seals plaintext under the Active key, returning ciphertext in
// auth-internals.md §2's exact `{key_id}:{nonce}:{ciphertext}` format —
// nonce and ciphertext each base64 (RawURLEncoding, no padding) so the
// colon delimiter can never collide with either segment's own bytes; a
// UUID key_id doesn't contain ":" either.
func (ks *RowKeySet) Encrypt(plaintext []byte) ([]byte, error) {
	gcm, err := gcmFor(ks.Active.Key)
	if err != nil {
		return nil, err
	}

	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("generate nonce: %w", err)
	}

	ciphertext := gcm.Seal(nil, nonce, plaintext, nil)
	encoded := fmt.Sprintf("%s:%s:%s",
		ks.Active.KeyID,
		base64.RawURLEncoding.EncodeToString(nonce),
		base64.RawURLEncoding.EncodeToString(ciphertext),
	)
	return []byte(encoded), nil
}

// Decrypt opens data, selecting the key by its embedded key_id — a
// Previous key still decrypts rows written before rotation, per
// auth-internals.md §2.
func (ks *RowKeySet) Decrypt(data []byte) ([]byte, error) {
	parts := strings.SplitN(string(data), ":", 3)
	if len(parts) != 3 {
		return nil, ErrMalformedCiphertext
	}
	keyID, nonceB64, ciphertextB64 := parts[0], parts[1], parts[2]

	key := ks.keyForID(keyID)
	if key == nil {
		return nil, ErrUnknownKeyID
	}

	nonce, err := base64.RawURLEncoding.DecodeString(nonceB64)
	if err != nil {
		return nil, fmt.Errorf("%w: decode nonce: %v", ErrMalformedCiphertext, err)
	}
	ciphertext, err := base64.RawURLEncoding.DecodeString(ciphertextB64)
	if err != nil {
		return nil, fmt.Errorf("%w: decode ciphertext: %v", ErrMalformedCiphertext, err)
	}

	gcm, err := gcmFor(key.Key)
	if err != nil {
		return nil, err
	}
	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return nil, fmt.Errorf("decrypt: %w", err)
	}
	return plaintext, nil
}

func (ks *RowKeySet) keyForID(keyID string) *RowKey {
	if ks.Active.KeyID == keyID {
		return &ks.Active
	}
	for i := range ks.Previous {
		if ks.Previous[i].KeyID == keyID {
			return &ks.Previous[i]
		}
	}
	return nil
}

func gcmFor(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("create AES cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("create GCM: %w", err)
	}
	return gcm, nil
}
