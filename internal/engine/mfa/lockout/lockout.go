// Package lockout implements the MFA attempt-counter/lockout behavior
// auth-internals.md §8 specifies for both POST /auth/mfa/verify and
// POST /auth/mfa/reverify: 5 consecutive failures for the same
// (user_id, tenant_id) lock further attempts out for 15 minutes. Shared
// by both endpoints since the doc explicitly calls for "the same
// attempt-counter/lockout behaviour" on reverify's failure path — one
// counter, one set of rules, not two copies that could drift.
package lockout

import (
	"context"
	"fmt"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/cache"
)

// MaxAttempts/Window are auth-internals.md §8's own "5 consecutive
// failures ... lock MFA for 15 minutes" numbers — deliberately a
// different threshold and window from loginflow's password brute-force
// lockout (10 failures/30 min), since they guard different attack
// surfaces (password guessing vs. MFA code guessing once the password is
// already known).
const (
	MaxAttempts = 5
	Window      = 15 * time.Minute
)

// lockedMarker is the sentinel value a key is set to once the lockout
// threshold is reached — distinguishing "locked" from "still counting"
// without a second Redis key.
const lockedMarker = "locked"

// Counter tracks failed MFA verification attempts per (user_id, tenant_id).
type Counter struct {
	cache *cache.Client
}

func NewCounter(cacheClient *cache.Client) *Counter {
	return &Counter{cache: cacheClient}
}

// key scopes the counter to (user_id, tenant_id), per auth-internals.md
// §8's "Attempt counter scope" — never to a per-ceremony/per-transaction
// identifier, since that would reset to zero on every fresh attempt and
// let an attacker who already has the password (or a valid session, for
// reverify) simply start over every 5 guesses.
func key(userID, tenantID string) string {
	return "auth:mfa_attempts:" + userID + ":" + tenantID
}

// Locked reports whether (userID, tenantID) is currently locked out.
func (c *Counter) Locked(ctx context.Context, userID, tenantID string) (bool, error) {
	value, found, err := c.cache.Get(ctx, key(userID, tenantID))
	if err != nil {
		return false, fmt.Errorf("check mfa lockout: %w", err)
	}
	return found && value == lockedMarker, nil
}

// RecordFailure increments the (user_id, tenant_id) counter and, once it
// reaches MaxAttempts, sets the lockout marker for Window.
func (c *Counter) RecordFailure(ctx context.Context, userID, tenantID string) error {
	count, err := c.cache.IncrWithTTL(ctx, key(userID, tenantID), Window)
	if err != nil {
		return fmt.Errorf("record mfa failure: %w", err)
	}
	if count >= MaxAttempts {
		if err := c.cache.SetWithTTL(ctx, key(userID, tenantID), lockedMarker, Window); err != nil {
			return fmt.Errorf("set mfa lockout marker: %w", err)
		}
	}
	return nil
}

// Reset clears (user_id, tenant_id)'s counter — called on a successful
// verification so a later, separate attempt doesn't inherit an
// in-progress failure count.
func (c *Counter) Reset(ctx context.Context, userID, tenantID string) error {
	if err := c.cache.Delete(ctx, key(userID, tenantID)); err != nil {
		return fmt.Errorf("reset mfa lockout counter: %w", err)
	}
	return nil
}
