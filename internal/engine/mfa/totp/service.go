// Package totp implements TOTP MFA setup and verification —
// auth-internals.md §8's "TOTP" section — on top of mfa.Store (the
// user_mfa row store, goerp#296) and rowcrypt (credential-at-rest
// encryption, goerp#297). Uses github.com/pquerna/otp, the library the
// doc's own reference implementation names, rather than a hand-rolled
// RFC 6238 implementation.
package totp

import (
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"time"
	"uuid"

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

	// auth-internals.md §8 "MFA enrollment": a pending secret lives 10
	// minutes, and the 5th wrong confirm code discards it.
	enrollmentTTL      = 10 * time.Minute
	maxConfirmAttempts = 5
)

var (
	ErrEnrollmentNotFound = errors.New("totp enrollment not found")
	ErrInvalidCode        = errors.New("invalid totp code")
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

// PendingEnrollment is a TOTP secret waiting for its first valid code
// (auth-internals.md §8 "MFA enrollment"). Secret is the base32 manual-entry
// key; QRSVG renders the same secret for scanning.
type PendingEnrollment struct {
	ID     string
	QRSVG  []byte
	Secret string
}

type pendingRecord struct {
	UserID string `json:"user_id"`
	Secret []byte `json:"secret"`
}

// BeginEnrollment generates a TOTP secret for userID and holds it,
// encrypted, in Redis for enrollmentTTL. Nothing is written to user_mfa
// until ConfirmEnrollment succeeds, so an abandoned setup never counts as
// an enrolled factor.
func (s *Service) BeginEnrollment(ctx context.Context, userID, accountName string) (*PendingEnrollment, error) {
	key, err := totp.Generate(totp.GenerateOpts{
		Issuer:      issuer,
		AccountName: accountName,
		Period:      period,
		SecretSize:  20,
		Digits:      otp.DigitsSix,
		Algorithm:   otp.AlgorithmSHA1,
	})
	if err != nil {
		return nil, fmt.Errorf("generate totp secret: %w", err)
	}

	ciphertext, err := s.keys.Encrypt([]byte(key.Secret()))
	if err != nil {
		return nil, fmt.Errorf("encrypt totp secret: %w", err)
	}
	record, err := json.Marshal(pendingRecord{UserID: userID, Secret: ciphertext})
	if err != nil {
		return nil, fmt.Errorf("encode pending enrollment: %w", err)
	}

	id := uuid.NewV7().String()
	if err := s.cache.SetWithTTL(ctx, enrollmentKey(id), string(record), enrollmentTTL); err != nil {
		return nil, fmt.Errorf("store pending enrollment: %w", err)
	}

	svg, err := qrCodeSVG(key.URL())
	if err != nil {
		return nil, fmt.Errorf("render totp qr code: %w", err)
	}

	return &PendingEnrollment{ID: id, QRSVG: svg, Secret: key.Secret()}, nil
}

// VerifiedEnrollment is a pending enrollment whose confirm code checked
// out. It still sits in Redis until ClaimEnrollment removes it.
type VerifiedEnrollment struct {
	id     string
	raw    string
	Secret []byte // rowcrypt-encrypted, ready to store as user_mfa.credential
}

// CheckEnrollmentCode checks code against userID's pending enrollment.
// ErrEnrollmentNotFound covers a missing, expired, or other user's
// enrollment; ErrInvalidCode a wrong or replayed code. The
// maxConfirmAttempts-th wrong code discards the enrollment. A match leaves
// the enrollment pending: the caller stores the factor and calls
// ClaimEnrollment inside the same transaction, so a failed write can be
// retried with the same enrollment.
func (s *Service) CheckEnrollmentCode(ctx context.Context, userID, enrollmentID, code string) (*VerifiedEnrollment, error) {
	raw, found, err := s.cache.Get(ctx, enrollmentKey(enrollmentID))
	if err != nil {
		return nil, fmt.Errorf("load pending enrollment: %w", err)
	}
	if !found {
		return nil, ErrEnrollmentNotFound
	}
	var record pendingRecord
	if err := json.Unmarshal([]byte(raw), &record); err != nil {
		return nil, fmt.Errorf("decode pending enrollment: %w", err)
	}
	if record.UserID != userID {
		return nil, ErrEnrollmentNotFound
	}

	secret, err := s.keys.Decrypt(record.Secret)
	if err != nil {
		return nil, fmt.Errorf("decrypt pending totp secret: %w", err)
	}
	valid, err := validate(code, string(secret), time.Now())
	if err != nil {
		return nil, err
	}
	if !valid {
		attempts, err := s.cache.IncrWithTTL(ctx, attemptsKey(enrollmentID), enrollmentTTL)
		if err != nil {
			return nil, fmt.Errorf("count enrollment attempts: %w", err)
		}
		if attempts >= maxConfirmAttempts {
			if err := s.discardEnrollment(ctx, enrollmentID); err != nil {
				return nil, err
			}
		}
		return nil, ErrInvalidCode
	}

	claimed, err := s.cache.SetNXWithTTL(ctx, replayKey(userID, code), "1", replayTTL)
	if err != nil {
		return nil, fmt.Errorf("claim totp replay slot: %w", err)
	}
	if !claimed {
		return nil, ErrInvalidCode
	}
	return &VerifiedEnrollment{id: enrollmentID, raw: raw, Secret: record.Secret}, nil
}

// ClaimEnrollment removes v's pending enrollment, returning
// ErrEnrollmentNotFound if a concurrent confirm claimed it first.
func (s *Service) ClaimEnrollment(ctx context.Context, v *VerifiedEnrollment) error {
	won, err := s.cache.DeleteIfEqual(ctx, enrollmentKey(v.id), v.raw)
	if err != nil {
		return fmt.Errorf("claim pending enrollment: %w", err)
	}
	if !won {
		return ErrEnrollmentNotFound
	}
	if err := s.cache.Delete(ctx, attemptsKey(v.id)); err != nil {
		return fmt.Errorf("clear enrollment attempts: %w", err)
	}
	return nil
}

func (s *Service) discardEnrollment(ctx context.Context, enrollmentID string) error {
	if err := s.cache.Delete(ctx, enrollmentKey(enrollmentID)); err != nil {
		return fmt.Errorf("discard pending enrollment: %w", err)
	}
	if err := s.cache.Delete(ctx, attemptsKey(enrollmentID)); err != nil {
		return fmt.Errorf("discard enrollment attempts: %w", err)
	}
	return nil
}

// validate reports whether code is valid for secret at now. A code of the
// wrong length is simply invalid; only a malformed stored secret is an error.
func validate(code, secret string, now time.Time) (bool, error) {
	valid, err := totp.ValidateCustom(code, secret, now, validateOpts)
	if errors.Is(err, otp.ErrValidateInputInvalidLength) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("validate totp code: %w", err)
	}
	return valid, nil
}

func enrollmentKey(id string) string { return "mfa:enroll:totp:" + id }

func attemptsKey(id string) string { return "mfa:enroll:totp:" + id + ":attempts" }

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

		validCode, err := validate(code, string(secret), now)
		if err != nil {
			return false, "", err
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
