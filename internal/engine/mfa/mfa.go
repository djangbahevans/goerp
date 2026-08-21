// Package mfa owns the MFA credential data model — auth-internals.md §2's
// user_mfa table: enrolled TOTP/WebAuthn/recovery-code factors per user.
// TOTP/WebAuthn/recovery-code-specific enrollment and verification logic
// (secret generation, ceremony handling, code hashing) is
// goerp#300/#301/#302's scope, not this package's — this package only
// provides the row store those tickets build on.
package mfa

import (
	"errors"
	"time"
)

var ErrCredentialNotFound = errors.New("mfa credential not found")

// CredentialType is a system.user_mfa row's type column.
type CredentialType string

const (
	CredentialTOTP         CredentialType = "totp"
	CredentialWebAuthn     CredentialType = "webauthn"
	CredentialRecoveryCode CredentialType = "recovery_code"
)

// Credential is one system.user_mfa row. Credential holds opaque,
// already-encrypted bytes (AES-256-GCM per auth-internals.md §8) —
// encrypting/decrypting it is the caller's job (goerp#297), not this
// store's, the same boundary session.Store draws around refresh_hash.
type Credential struct {
	ID         string
	UserID     string
	Type       CredentialType
	Credential []byte
	Label      *string
	IsPrimary  bool
	CreatedAt  time.Time
	LastUsedAt *time.Time
	RevokedAt  *time.Time
}
