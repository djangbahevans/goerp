// Package sessionrevoke revokes sessions immediately: it marks the
// session row revoked and blocklists its id in Redis for the access
// token's max lifetime, so an already-issued, not-yet-expired access
// token stops working the moment its session is revoked rather than
// waiting out its own exp claim (auth-internals.md §4 "Token revocation").
// Checking the blocklist on every request is a separate ticket (goerp#88,
// the auth middleware pipeline) — this package only maintains it.
package sessionrevoke

import (
	"context"
	"fmt"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/cache"
	"github.com/djangbahevans/goerp/internal/engine/session"
)

// blocklistTTL matches the access token's max lifetime
// (authtoken.accessTokenTTL) — auth-internals.md §4: after that TTL, any
// token for the session would have expired naturally anyway, so the
// blocklist entry can safely drop.
const blocklistTTL = 15 * time.Minute

func blocklistKey(sessionID string) string {
	return "auth:session:revoked:" + sessionID
}

type Revoker struct {
	sessions *session.Store
	cache    *cache.Client
}

func NewRevoker(sessions *session.Store, cacheClient *cache.Client) *Revoker {
	return &Revoker{sessions: sessions, cache: cacheClient}
}

// Revoke marks sessionID revoked and blocklists it in Redis.
func (r *Revoker) Revoke(ctx context.Context, sessionID, reason string) error {
	if err := r.sessions.Revoke(ctx, sessionID, reason); err != nil {
		return err
	}
	if err := r.cache.SetWithTTL(ctx, blocklistKey(sessionID), "1", blocklistTTL); err != nil {
		return fmt.Errorf("blocklist session %s: %w", sessionID, err)
	}
	return nil
}

// RevokeAllForUser revokes every non-revoked session for userID and
// blocklists each one. Sessions read before the revoking UPDATE runs is
// what lets every one of them also get blocklisted — the bulk UPDATE
// itself can't hand back which rows it touched.
func (r *Revoker) RevokeAllForUser(ctx context.Context, userID, reason string) error {
	ids, err := r.sessions.NonRevokedIDsForUser(ctx, userID)
	if err != nil {
		return err
	}

	if err := r.sessions.RevokeAllForUser(ctx, userID, reason); err != nil {
		return err
	}

	for _, id := range ids {
		if err := r.cache.SetWithTTL(ctx, blocklistKey(id), "1", blocklistTTL); err != nil {
			return fmt.Errorf("blocklist session %s: %w", id, err)
		}
	}

	return nil
}

// RevokeAllForTenant revokes every non-revoked session for tenantID and
// blocklists each one — what a tenant suspend calls, distinct from
// RevokeAllForUser's per-user scope.
func (r *Revoker) RevokeAllForTenant(ctx context.Context, tenantID, reason string) error {
	ids, err := r.sessions.NonRevokedIDsForTenant(ctx, tenantID)
	if err != nil {
		return err
	}

	if err := r.sessions.RevokeAllForTenant(ctx, tenantID, reason); err != nil {
		return err
	}

	for _, id := range ids {
		if err := r.cache.SetWithTTL(ctx, blocklistKey(id), "1", blocklistTTL); err != nil {
			return fmt.Errorf("blocklist session %s: %w", id, err)
		}
	}

	return nil
}

// IsBlocked reports whether sessionID is currently blocklisted — the
// check a future auth middleware (goerp#88) runs on every request, keyed
// by the access token's sid claim.
func (r *Revoker) IsBlocked(ctx context.Context, sessionID string) (bool, error) {
	blocked, err := r.cache.Exists(ctx, blocklistKey(sessionID))
	if err != nil {
		return false, fmt.Errorf("check blocklist for session %s: %w", sessionID, err)
	}
	return blocked, nil
}
