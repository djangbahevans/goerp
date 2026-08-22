// Package totp implements TOTP MFA setup and verification —
// auth-internals.md §8's "TOTP" section — on top of mfa.Store (the
// user_mfa row store, goerp#296) and rowcrypt (credential-at-rest
// encryption, goerp#297). Uses github.com/pquerna/otp, the library the
// doc's own reference implementation names, rather than a hand-rolled
// RFC 6238 implementation.
package totp

import (
	"context"
	"fmt"
	"time"

	"github.com/pquerna/otp"
	"github.com/pquerna/otp/totp"

	"github.com/djangbahevans/goerp/internal/engine/auth/rowcrypt"
	"github.com/djangbahevans/goerp/internal/engine/cache"
	"github.com/djangbahevans/goerp/internal/engine/mfa"
)

// issuer is the "GoERP" literal auth-internals.md §8's own TOTP example
// hardcodes as GenerateOpts.Issuer — not made configurable since nothing
// in the doc suggests it varies per tenant or deployment.
const issuer = "GoERP"

const (
	period = 30 // seconds, per auth-internals.md §8
	skew   = 1  // ± 1 window = ±30s tolerance

	// replayTTL matches the 30s period × the ±1 skew window each side —
	// auth-internals.md §8 states this directly as "90 seconds".
	replayTTL = 90 * time.Second
)

var validateOpts = totp.ValidateOpts{
	Period:    period,
	Skew:      skew,
	Digits:    otp.DigitsSix,
	Algorithm: otp.AlgorithmSHA1,
}

// Service ties together credential storage, at-rest encryption, and Redis
// replay protection to implement TOTP enrollment and verification.
type Service struct {
	store *mfa.Store
	keys  *rowcrypt.RowKeySet
	cache *cache.Client
}

func NewService(store *mfa.Store, keys *rowcrypt.RowKeySet, cacheClient *cache.Client) *Service {
	return &Service{store: store, keys: keys, cache: cacheClient}
}

// Enroll generates a new TOTP secret for userID, encrypts and persists it
// as a user_mfa row, and renders a server-side SVG QR code for the user's
// authenticator app — never a URL that logs the secret, per
// auth-internals.md §8.
func (s *Service) Enroll(ctx context.Context, userID, accountName string, label *string) (qrSVG []byte, cred *mfa.Credential, err error) {
	key, err := totp.Generate(totp.GenerateOpts{
		Issuer:      issuer,
		AccountName: accountName,
		Period:      period,
		SecretSize:  20,
		Digits:      otp.DigitsSix,
		Algorithm:   otp.AlgorithmSHA1,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("generate totp secret: %w", err)
	}

	ciphertext, err := s.keys.Encrypt([]byte(key.Secret()))
	if err != nil {
		return nil, nil, fmt.Errorf("encrypt totp secret: %w", err)
	}

	cred, err = s.store.Insert(ctx, userID, mfa.CredentialTOTP, ciphertext, label)
	if err != nil {
		return nil, nil, fmt.Errorf("store totp credential: %w", err)
	}

	svg, err := qrCodeSVG(key.URL())
	if err != nil {
		return nil, nil, fmt.Errorf("render totp qr code: %w", err)
	}

	return svg, cred, nil
}

// Verify reports whether code matches one of userID's enrolled TOTP
// factors within the current ±1 window, and if so, the matched
// credential's user_mfa row ID — the caller's mfa_credential_id (session
// row and access token claim, auth-internals.md §4/§8). A code already
// accepted within the last 90 seconds is rejected even if it's still
// cryptographically valid — auth-internals.md §8's replay window, claimed
// atomically via Redis SETNX so two concurrent verify calls for the same
// code can't both succeed. The bool return carries the verification
// outcome; error is reserved for infrastructure failures
// (DB/Redis/decrypt), not a wrong or replayed code.
func (s *Service) Verify(ctx context.Context, userID, code string) (valid bool, credentialID string, err error) {
	creds, err := s.store.ListActiveByUser(ctx, userID)
	if err != nil {
		return false, "", fmt.Errorf("list mfa credentials: %w", err)
	}

	now := time.Now()
	var decryptErr error
	for _, c := range creds {
		if c.Type != mfa.CredentialTOTP {
			continue
		}

		secret, err := s.keys.Decrypt(c.Credential)
		if err != nil {
			// A single corrupted or unrecognized-key credential must not
			// lock out a user who has another, decryptable TOTP factor
			// enrolled — try the rest of their factors first. Remembered
			// so that if every candidate turns out undecryptable, the
			// call still surfaces this as an error rather than silently
			// reporting "wrong code" for what's actually a systemic
			// problem (e.g. a misconfigured key set).
			decryptErr = fmt.Errorf("decrypt totp credential %s: %w", c.ID, err)
			continue
		}

		validCode, err := totp.ValidateCustom(code, string(secret), now, validateOpts)
		if err != nil {
			return false, "", fmt.Errorf("validate totp code: %w", err)
		}
		if !validCode {
			continue
		}

		claimed, err := s.cache.SetNXWithTTL(ctx, replayKey(userID, code), "1", replayTTL)
		if err != nil {
			return false, "", fmt.Errorf("claim totp replay slot: %w", err)
		}
		if !claimed {
			return false, "", nil
		}
		return true, c.ID, nil
	}

	return false, "", decryptErr
}

// replayKey matches auth-internals.md §8's totp:used:{user_id}:{code}
// format exactly — scoped per-user (not a bare {code}) since a 6-digit
// TOTP code is short enough that two different users could otherwise
// spuriously collide in the same window.
func replayKey(userID, code string) string {
	return "totp:used:" + userID + ":" + code
}
