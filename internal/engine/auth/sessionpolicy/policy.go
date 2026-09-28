// Package sessionpolicy is a tenant's session lifetime bounds, set on the
// tenant settings page (shell-ux.md §5.5 "Security"): an idle timeout and
// an absolute maximum session length, both stored in tenantconfig.
// authtoken.Issuer applies them to every session row's expires_at, at
// login and on each refresh rotation, and session.Store.Rotate refuses a
// row past its expires_at, so a session ends once either bound is hit.
package sessionpolicy

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/rs/zerolog/log"
)

// Tenant config keys, each a whole number of minutes. "0" or no value
// leaves that bound off.
const (
	KeyIdleTimeout = "auth.session_idle_timeout"
	KeyAbsoluteMax = "auth.session_absolute_max"
)

// Accepted ranges. The idle timeout can't go below the access token's
// 15-minute lifetime: activity is only seen at refresh, so a shorter idle
// window couldn't be enforced any tighter than that anyway.
const (
	MinIdleTimeout = 15 * time.Minute
	MaxIdleTimeout = 30 * 24 * time.Hour
	MinAbsoluteMax = time.Hour
	MaxAbsoluteMax = 365 * 24 * time.Hour
)

var ErrInvalid = errors.New("invalid session policy")

// Policy is one tenant's session lifetime bounds. A zero field is unset.
type Policy struct {
	// IdleTimeout ends a session this long after its last refresh.
	IdleTimeout time.Duration
	// AbsoluteMax ends a session this long after its login, however
	// active it stays.
	AbsoluteMax time.Duration
}

// Validate reports whether p's bounds are in range and whole minutes.
func (p Policy) Validate() error {
	if p.IdleTimeout != 0 && (p.IdleTimeout < MinIdleTimeout || p.IdleTimeout > MaxIdleTimeout || p.IdleTimeout%time.Minute != 0) {
		return fmt.Errorf("%w: idle timeout must be whole minutes between %d and %d", ErrInvalid, int(MinIdleTimeout.Minutes()), int(MaxIdleTimeout.Minutes()))
	}
	if p.AbsoluteMax != 0 && (p.AbsoluteMax < MinAbsoluteMax || p.AbsoluteMax > MaxAbsoluteMax || p.AbsoluteMax%time.Minute != 0) {
		return fmt.Errorf("%w: absolute maximum must be whole minutes between %d and %d", ErrInvalid, int(MinAbsoluteMax.Minutes()), int(MaxAbsoluteMax.Minutes()))
	}
	if p.IdleTimeout != 0 && p.AbsoluteMax != 0 && p.AbsoluteMax < p.IdleTimeout {
		return fmt.Errorf("%w: absolute maximum must not be shorter than the idle timeout", ErrInvalid)
	}
	return nil
}

// ExpiresAt is when a session row written at now ends: ttl from now (the
// refresh token's own lifetime), cut short by the idle timeout and by the
// absolute maximum counted from familyStart, the family's login.
func (p Policy) ExpiresAt(now, familyStart time.Time, ttl time.Duration) time.Time {
	end := now.Add(ttl)
	if p.IdleTimeout > 0 {
		end = earliest(end, now.Add(p.IdleTimeout))
	}
	if p.AbsoluteMax > 0 {
		end = earliest(end, familyStart.Add(p.AbsoluteMax))
	}
	return end
}

func earliest(a, b time.Time) time.Time {
	if b.Before(a) {
		return b
	}
	return a
}

// Config is satisfied by tenantconfig.Store. This package takes the
// interface rather than importing tenantconfig, so authtoken can depend
// on it without pulling in tenantconfig's module-registry imports.
type Config interface {
	GetPrefix(ctx context.Context, tenantID, prefix string) (map[string]string, error)
	SetMany(ctx context.Context, tenantID string, values map[string]string) error
}

type Store struct {
	config Config
}

func NewStore(config Config) *Store {
	return &Store{config: config}
}

// Load reads tenantID's policy. An unparseable or out-of-range stored
// value leaves that bound off, with a warning, like password.PolicyStore.
func (s *Store) Load(ctx context.Context, tenantID string) (Policy, error) {
	values, err := s.config.GetPrefix(ctx, tenantID, "auth.session_")
	if err != nil {
		return Policy{}, fmt.Errorf("load session policy: %w", err)
	}
	p := Policy{
		IdleTimeout: parseMinutes(tenantID, values, KeyIdleTimeout),
		AbsoluteMax: parseMinutes(tenantID, values, KeyAbsoluteMax),
	}
	if err := p.Validate(); err != nil {
		log.Warn().Err(err).Str("tenant_id", tenantID).Msg("sessionpolicy: stored policy out of range, ignoring it")
		return Policy{}, nil
	}
	return p, nil
}

// Save writes p as tenantID's policy in one transaction.
func (s *Store) Save(ctx context.Context, tenantID string, p Policy) error {
	if err := p.Validate(); err != nil {
		return err
	}
	if err := s.config.SetMany(ctx, tenantID, map[string]string{
		KeyIdleTimeout: strconv.Itoa(int(p.IdleTimeout.Minutes())),
		KeyAbsoluteMax: strconv.Itoa(int(p.AbsoluteMax.Minutes())),
	}); err != nil {
		return fmt.Errorf("save session policy: %w", err)
	}
	return nil
}

func parseMinutes(tenantID string, values map[string]string, key string) time.Duration {
	v, ok := values[key]
	if !ok {
		return 0
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 0 {
		log.Warn().Str("tenant_id", tenantID).Str("key", key).Str("value", v).Msg("sessionpolicy: invalid value, ignoring it")
		return 0
	}
	return time.Duration(n) * time.Minute
}
