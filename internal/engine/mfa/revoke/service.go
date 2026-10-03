// Package revoke removes MFA factors and invalidates sessions in the affected scope.
package revoke

import (
	"context"
	"database/sql"
	"errors"
	"slices"

	"github.com/rs/zerolog/log"

	"github.com/djangbahevans/goerp/internal/engine/auth/sessionrevoke"
	"github.com/djangbahevans/goerp/internal/engine/mfa"
)

// Factor removal is a security event.
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

// RevokeFactor serializes the last-factor policy check with revocation.
// Platform removal revokes every tenant session; tenant removal revokes only that tenant.
// Recovery codes are revoked when their scope loses its last sign-in factor.
func (s *Service) RevokeFactor(ctx context.Context, userID, tenantID, credID string, policyApplies bool) error {
	var sessionIDs []string
	err := s.mfaStore.WithTx(ctx, func(tx *sql.Tx) error {
		// Serializes against a concurrent removal of the user's other
		// factor, which would otherwise let both pass the last-factor check.
		if err := s.mfaStore.LockUserTx(ctx, tx, userID); err != nil {
			return err
		}

		creds, err := s.mfaStore.ListAcceptedTx(ctx, tx, userID, mfa.Scope{TenantID: tenantID})
		if err != nil {
			return err
		}

		var target *mfa.Credential
		remaining := 0
		for _, c := range creds {
			if !c.Type.IsFactor() {
				continue
			}

			if c.ID == credID {
				target = c
			} else {
				remaining++
			}
		}

		if target == nil {
			return mfa.ErrCredentialNotFound
		}

		if remaining == 0 && policyApplies {
			return ErrRequiredByPolicy
		}

		if err := s.mfaStore.RevokeTx(ctx, tx, userID, credID); err != nil {
			return err
		}

		all, err := s.mfaStore.ListActiveByUserTx(ctx, tx, userID)
		if err != nil {
			return err
		}

		scopeRemaining := slices.ContainsFunc(all, func(c *mfa.Credential) bool {
			sameScope := c.TenantID == nil && target.TenantID == nil ||
				c.TenantID != nil && target.TenantID != nil && *c.TenantID == *target.TenantID
			return c.Type.IsFactor() && sameScope
		})

		if !scopeRemaining {
			if err := s.mfaStore.RevokeRecoveryCodesTx(ctx, tx, userID, target.TenantID); err != nil {
				return err
			}
		}

		if target.TenantID == nil {
			sessionIDs, err = s.sessions.RevokeAllForUserTx(ctx, tx, userID, reason)
		} else {
			sessionIDs, err = s.sessions.RevokeAllForUserInTenantTx(ctx, tx, userID, tenantID, reason)
		}

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
