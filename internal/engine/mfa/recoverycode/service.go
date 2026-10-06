// Package recoverycode generates and consumes scoped MFA recovery codes.
package recoverycode

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"

	"golang.org/x/crypto/bcrypt"

	"github.com/djangbahevans/goerp/internal/engine/mfa"
)

// hashCost is the bcrypt cost of new recovery code hashes. Recovery codes are
// one-time credentials; cost 12 bounds offline guessing.
var hashCost = 12

// SetHashCost changes the bcrypt cost new recovery code hashes use and returns
// a function that restores the previous cost. Hashing a batch at the default
// cost takes seconds, and far longer under the race detector, so test
// binaries lower it; stored hashes record their own cost and still verify.
// Production code never calls it, and it must not run concurrently with
// hashing.
func SetHashCost(cost int) (restore func()) {
	if cost < bcrypt.MinCost || cost > bcrypt.MaxCost {
		panic(fmt.Sprintf("recoverycode.SetHashCost: cost %d is outside %d-%d", cost, bcrypt.MinCost, bcrypt.MaxCost))
	}
	previous := hashCost
	hashCost = cost
	return func() { hashCost = previous }
}

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
// codes at the default cost takes seconds, so callers do it before opening a
// transaction and store the result with InsertTx.
func Prepare() (Set, error) {
	codes, err := GenerateCodes()
	if err != nil {
		return Set{}, err
	}

	hashes := make([][]byte, len(codes))
	for i, code := range codes {
		hashes[i], err = bcrypt.GenerateFromPassword([]byte(code), hashCost)
		if err != nil {
			return Set{}, fmt.Errorf("hash recovery code: %w", err)
		}
	}

	return Set{Codes: codes, Hashes: hashes}, nil
}

func (s *Service) InsertTx(ctx context.Context, tx *sql.Tx, userID string, tenantID *string, set Set) error {
	for _, hash := range set.Hashes {
		if _, err := s.store.InsertScopedTx(ctx, tx, userID, tenantID, mfa.CredentialRecoveryCode, hash, nil); err != nil {
			return fmt.Errorf("store recovery code: %w", err)
		}
	}

	return nil
}

// ErrNotEnrolled is returned by Regenerate for a user without an active
// TOTP or WebAuthn factor.
var ErrNotEnrolled = errors.New("mfa not enrolled")

// RegenerateTx requires a remaining sign-in factor in the same scope.
// The account lock prevents concurrent removal from leaving recovery codes alone.
func (s *Service) RegenerateTx(ctx context.Context, tx *sql.Tx, userID string, tenantID *string, set Set) error {
	if err := s.store.LockUserTx(ctx, tx, userID); err != nil {
		return err
	}

	creds, err := s.store.ListActiveByUserTx(ctx, tx, userID)
	if err != nil {
		return err
	}

	if !slices.ContainsFunc(creds, func(c *mfa.Credential) bool {
		return c.Type.IsFactor() && sameScope(c.TenantID, tenantID)
	}) {
		return ErrNotEnrolled
	}

	if err := s.store.RevokeRecoveryCodesTx(ctx, tx, userID, tenantID); err != nil {
		return err
	}

	return s.InsertTx(ctx, tx, userID, tenantID, set)
}

// Verify consumes a matching accepted code atomically; concurrent callers cannot both succeed.
func (s *Service) Verify(ctx context.Context, userID, code string, scope mfa.Scope) (valid bool, credentialID string, err error) {
	creds, err := s.store.ListAccepted(ctx, userID, scope)
	if err != nil {
		return false, "", fmt.Errorf("list mfa credentials: %w", err)
	}

	return verify(ctx, creds, code, s.store.ConsumeOnce)
}

// VerifyTx consumes the proof in the transaction that creates or updates
// the session, so a failed session write leaves the code usable.
func (s *Service) VerifyTx(ctx context.Context, tx *sql.Tx, userID, code string, scope mfa.Scope) (bool, string, error) {
	creds, err := s.store.ListAcceptedTx(ctx, tx, userID, scope)
	if err != nil {
		return false, "", fmt.Errorf("list mfa credentials: %w", err)
	}

	return verify(ctx, creds, code, func(ctx context.Context, id string) error {
		return s.store.ConsumeOnceTx(ctx, tx, id)
	})
}

func verify(ctx context.Context, creds []*mfa.Credential, code string, consume func(context.Context, string) error) (bool, string, error) {
	for _, c := range creds {
		if c.Type != mfa.CredentialRecoveryCode {
			continue
		}

		if err := bcrypt.CompareHashAndPassword(c.Credential, []byte(code)); err != nil {
			continue
		}

		if err := consume(ctx, c.ID); err != nil {
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

func sameScope(a, b *string) bool {
	return a == nil && b == nil || a != nil && b != nil && *a == *b
}
