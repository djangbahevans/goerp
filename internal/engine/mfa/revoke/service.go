// Package revoke implements the MFA-factor-revocation → session-
// invalidation cascade — auth-internals.md §8's "Invalidation on factor
// change": revoking any MFA factor revokes all of the user's active
// sessions; enrolling a new factor while others remain active revokes
// nothing (mfa.Store.Insert never touches sessions, so that half of the
// rule holds by construction — nothing in this package needs to enforce
// it separately).
package revoke

import (
	"context"
	"fmt"

	"github.com/djangbahevans/goerp/internal/engine/auth/sessionrevoke"
	"github.com/djangbahevans/goerp/internal/engine/mfa"
)

// reason matches auth-internals.md §4's revoke_reason vocabulary
// ('logout', 'password_change', 'security_event', 'admin') — an MFA
// factor being removed is a security event, not a new category of its
// own.
const reason = "security_event"

type Service struct {
	mfaStore *mfa.Store
	sessions *sessionrevoke.Revoker
}

func NewService(mfaStore *mfa.Store, sessions *sessionrevoke.Revoker) *Service {
	return &Service{mfaStore: mfaStore, sessions: sessions}
}

// RevokeFactor revokes credID and, on success, revokes every one of
// userID's active sessions. Verifies credID is actually one of userID's
// own active factors before doing either — callers pass both ids, but
// this method doesn't trust that pairing blindly, since a mismatched
// pair would revoke the wrong user's sessions. Returns
// mfa.ErrCredentialNotFound uniformly whether credID doesn't exist,
// isn't userID's, or was already revoked — the caller can't distinguish
// those cases from this method, avoiding a confirm/deny oracle over
// another user's credential ids.
func (s *Service) RevokeFactor(ctx context.Context, userID, credID string) error {
	creds, err := s.mfaStore.ListActiveByUser(ctx, userID)
	if err != nil {
		return fmt.Errorf("list mfa credentials: %w", err)
	}

	owned := false
	for _, c := range creds {
		if c.ID == credID {
			owned = true
			break
		}
	}
	if !owned {
		return mfa.ErrCredentialNotFound
	}

	if err := s.mfaStore.Revoke(ctx, credID); err != nil {
		return fmt.Errorf("revoke mfa factor: %w", err)
	}

	if err := s.sessions.RevokeAllForUser(ctx, userID, reason); err != nil {
		return fmt.Errorf("revoke sessions after mfa factor revocation: %w", err)
	}

	return nil
}
