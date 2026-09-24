// Package recoverycode implements MFA recovery-code generation and
// consumption — auth-internals.md §8's "Recovery codes" section — on top
// of mfa.Store (the user_mfa row store, goerp#296).
package recoverycode

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"golang.org/x/crypto/bcrypt"

	"github.com/djangbahevans/goerp/internal/engine/mfa"
)

// bcryptCost matches auth-internals.md §8's own example — a one-time-use
// credential, so bcrypt's slowness (unlike a login-path password hash) is
// an acceptable, deliberate cost rather than something to tune down.
const bcryptCost = 12

type Service struct {
	store *mfa.Store
}

func NewService(store *mfa.Store) *Service {
	return &Service{store: store}
}

// Enroll generates a fresh set of recovery codes for userID, storing each
// as its own bcrypt-hashed user_mfa row, and returns the plaintext codes
// — shown to the user exactly once; nothing in this package persists or
// logs them again after this call returns.
func (s *Service) Enroll(ctx context.Context, userID string) ([]string, error) {
	set, err := Prepare()
	if err != nil {
		return nil, err
	}
	for _, hash := range set.Hashes {
		if _, err := s.store.Insert(ctx, userID, mfa.CredentialRecoveryCode, hash, nil); err != nil {
			return nil, fmt.Errorf("store recovery code: %w", err)
		}
	}
	return set.Codes, nil
}

// Set is a freshly generated batch of recovery codes and their bcrypt
// hashes, index-aligned.
type Set struct {
	Codes  []string
	Hashes [][]byte
}

// Prepare generates and hashes a new batch without storing it. Hashing ten
// codes at bcryptCost takes seconds, so callers do it before opening a
// transaction and store the result with InsertTx.
func Prepare() (Set, error) {
	codes, err := GenerateCodes()
	if err != nil {
		return Set{}, err
	}
	hashes := make([][]byte, len(codes))
	for i, code := range codes {
		hashes[i], err = bcrypt.GenerateFromPassword([]byte(code), bcryptCost)
		if err != nil {
			return Set{}, fmt.Errorf("hash recovery code: %w", err)
		}
	}
	return Set{Codes: codes, Hashes: hashes}, nil
}

// InsertTx stores set's hashes as userID's recovery codes inside the
// caller's transaction.
func (s *Service) InsertTx(ctx context.Context, tx *sql.Tx, userID string, set Set) error {
	for _, hash := range set.Hashes {
		if _, err := s.store.InsertTx(ctx, tx, userID, mfa.CredentialRecoveryCode, hash, nil); err != nil {
			return fmt.Errorf("store recovery code: %w", err)
		}
	}
	return nil
}

// Verify checks code against userID's enrolled, non-revoked recovery
// codes, and if matched, consumes it and returns its user_mfa row ID —
// the caller's mfa_credential_id (session row and access token claim,
// auth-internals.md §4/§8). The match is consumed atomically via
// mfa.Store.ConsumeOnce, so two concurrent Verify calls for the same code
// cannot both succeed — see ConsumeOnce's own doc comment for why this
// differs from Store.Revoke.
func (s *Service) Verify(ctx context.Context, userID, code string) (valid bool, credentialID string, err error) {
	creds, err := s.store.ListActiveByUser(ctx, userID)
	if err != nil {
		return false, "", fmt.Errorf("list mfa credentials: %w", err)
	}

	for _, c := range creds {
		if c.Type != mfa.CredentialRecoveryCode {
			continue
		}
		if err := bcrypt.CompareHashAndPassword(c.Credential, []byte(code)); err != nil {
			continue
		}

		if err := s.store.ConsumeOnce(ctx, c.ID); err != nil {
			if errors.Is(err, mfa.ErrCredentialNotFound) {
				// Consumed by a concurrent Verify call between our list
				// and this ConsumeOnce — the code was raced away, not a
				// system failure.
				return false, "", nil
			}
			return false, "", fmt.Errorf("consume recovery code: %w", err)
		}
		return true, c.ID, nil
	}

	return false, "", nil
}
