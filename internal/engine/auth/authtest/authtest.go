// Package authtest provides process-local signing and encryption key sets for
// tests that need keys but not the system.jwt_signing_keys,
// system.mfa_token_signing_keys or system.row_encryption_keys tables. Loading
// keys through the stores shares one active row per table across every test
// package running against the same Postgres, so tests that do must serialize
// on a global lock; these sets never touch the database.
//
// Importing this package also lowers the bcrypt cost of recovery code hashes
// for the test binary: hashing a batch at the production cost takes seconds,
// and far longer under the race detector.
package authtest

import (
	"crypto/rand"
	"crypto/rsa"
	"sync"
	"time"
	"uuid"

	"golang.org/x/crypto/bcrypt"

	"github.com/djangbahevans/goerp/internal/engine/auth/mfatoken"
	"github.com/djangbahevans/goerp/internal/engine/auth/rowcrypt"
	"github.com/djangbahevans/goerp/internal/engine/auth/signingkey"
	"github.com/djangbahevans/goerp/internal/engine/mfa/recoverycode"
)

func init() { recoverycode.SetHashCost(bcrypt.MinCost) }

const ephemeralVersion = "ephemeral"

var (
	signingKeys  = sync.OnceValue(newSigningKeySet)
	mfaTokenKeys = sync.OnceValue(newMFATokenKeySet)
	rowKeys      = sync.OnceValue(newRowKeySet)
)

// SigningKeys returns a JWT signing key set. The RSA key is generated once
// per process; each call returns its own copy of the set.
func SigningKeys() *signingkey.SigningKeySet {
	return new(*signingKeys())
}

// MFATokenKeys returns an mfa_token signing key set, generated once per
// process.
func MFATokenKeys() *mfatoken.KeySet {
	return new(*mfaTokenKeys())
}

// RowKeys returns a row-encryption key set, generated once per process.
func RowKeys() *rowcrypt.RowKeySet {
	return new(*rowKeys())
}

func newSigningKeySet() *signingkey.SigningKeySet {
	private, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		panic("authtest: generate RSA-2048 key pair: " + err.Error())
	}
	now := time.Now()
	return &signingkey.SigningKeySet{Active: signingkey.SigningKey{
		KID:                  uuid.New().String(),
		Algorithm:            "RS256",
		Private:              private,
		Public:               &private.PublicKey,
		CreatedAt:            now,
		ExpiresAt:            now.Add(90 * 24 * time.Hour),
		SecretManagerVersion: ephemeralVersion,
	}}
}

func newMFATokenKeySet() *mfatoken.KeySet {
	return &mfatoken.KeySet{Active: mfatoken.Key{
		KeyID:                uuid.New().String(),
		Secret:               randomKey(),
		CreatedAt:            time.Now(),
		SecretManagerVersion: ephemeralVersion,
	}}
}

func newRowKeySet() *rowcrypt.RowKeySet {
	return &rowcrypt.RowKeySet{Active: rowcrypt.RowKey{
		KeyID:                uuid.New().String(),
		Key:                  randomKey(),
		CreatedAt:            time.Now(),
		SecretManagerVersion: ephemeralVersion,
	}}
}

func randomKey() []byte {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		panic("authtest: generate key: " + err.Error())
	}
	return key
}
