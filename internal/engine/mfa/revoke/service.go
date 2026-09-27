// Package revoke implements removing an MFA factor and its session-
// invalidation cascade — auth-internals.md §8 "Managing factors" and
// "Invalidation on factor change": removing any factor revokes all of the
// user's active sessions; enrolling a new factor while others remain
// active revokes nothing (mfa.Store.Insert never touches sessions, so that
// half of the rule holds by construction).
package revoke

import (
	"context"
	"database/sql"
	"errors"

	"github.com/rs/zerolog/log"

	"github.com/djangbahevans/goerp/internal/engine/auth/sessionrevoke"
	"github.com/djangbahevans/goerp/internal/engine/mfa"
)

// reason matches auth-internals.md §4's revoke_reason vocabulary
// ('logout', 'password_change', 'security_event', 'admin') — an MFA
// factor being removed is a security event, not a new category of its
// own.
const reason = "security_event"

// ErrRequiredByPolicy is returned for the last TOTP/WebAuthn factor of a
// user the tenant's MFA policy applies to.
var ErrRequiredByPolicy = errors.New("mfa factor required by policy")

type Service struct {
	mfaStore *mfa.Store
	sessions *sessionrevoke.Revoker
}

func NewService(mfaStore *mfa.Store, sessions *sessionrevoke.Revoker) *Service {
	return &Service{mfaStore: mfaStore, sessions: sessions}
}

// RevokeFactor revokes credID, one of userID's active TOTP/WebAuthn
// factors, and every one of userID's sessions in every tenant, in one
// transaction, then blocklists those sessions. When credID is the user's
// last factor, it also revokes every recovery code, since recovery codes
// alone would otherwise still count as an enrolled factor for MFA
// enforcement — unless policyApplies, in which case nothing changes and
// ErrRequiredByPolicy is returned.
//
// Returns mfa.ErrCredentialNotFound uniformly whether credID doesn't
// exist, isn't userID's, isn't a TOTP/WebAuthn factor, or was already
// revoked, so the caller has no confirm/deny oracle over another user's
// credential ids.
func (s *Service) RevokeFactor(ctx context.Context, userID, credID string, policyApplies bool) error {
	var sessionIDs []string
	err := s.mfaStore.WithTx(ctx, func(tx *sql.Tx) error {
		// Serializes against a concurrent removal of the user's other
		// factor, which would otherwise let both pass the last-factor check.
		if err := s.mfaStore.LockUserTx(ctx, tx, userID); err != nil {
			return err
		}
		creds, err := s.mfaStore.ListActiveByUserTx(ctx, tx, userID)
		if err != nil {
			return err
		}
		found, remaining := false, 0
		for _, c := range creds {
			if !c.Type.IsFactor() {
				continue
			}
			if c.ID == credID {
				found = true
				continue
			}
			remaining++
		}
		if !found {
			return mfa.ErrCredentialNotFound
		}
		if remaining == 0 && policyApplies {
			return ErrRequiredByPolicy
		}
		if err := s.mfaStore.RevokeTx(ctx, tx, userID, credID); err != nil {
			return err
		}
		if remaining == 0 {
			if err := s.mfaStore.RevokeAllOfTypeTx(ctx, tx, userID, mfa.CredentialRecoveryCode); err != nil {
				return err
			}
		}
		sessionIDs, err = s.sessions.RevokeAllForUserTx(ctx, tx, userID, reason)
		return err
	})
	if err != nil {
		return err
	}
	// The removal is committed and the sessions can no longer refresh, so
	// a blocklist failure only lets their access tokens run to expiry.
	if err := s.sessions.Blocklist(ctx, sessionIDs); err != nil {
		log.Error().Err(err).Str("user_id", userID).Msg("revoke: blocklist sessions after mfa factor revocation failed")
	}
	return nil
}
