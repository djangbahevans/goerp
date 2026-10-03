// Package mfa stores platform and tenant MFA credentials and applies tenant acceptance rules.
package mfa

import (
	"errors"
	"time"
)

var ErrCredentialNotFound = errors.New("mfa credential not found")

type CredentialType string

const (
	CredentialTOTP         CredentialType = "totp"
	CredentialWebAuthn     CredentialType = "webauthn"
	CredentialRecoveryCode CredentialType = "recovery_code"
)

// Credential.Credential holds an encrypted factor payload or a bcrypt recovery-code hash.
// TenantID is nil for a platform credential.
type Credential struct {
	ID         string
	UserID     string
	TenantID   *string
	Type       CredentialType
	Credential []byte
	Label      *string
	IsPrimary  bool
	CreatedAt  time.Time
	LastUsedAt *time.Time
	RevokedAt  *time.Time
}

type Scope struct {
	TenantID     string
	PlatformOnly bool
}

func (t CredentialType) IsFactor() bool {
	return t == CredentialTOTP || t == CredentialWebAuthn
}
