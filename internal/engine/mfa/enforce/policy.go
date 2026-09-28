// Package enforce implements auth-internals.md §8 "MFA enforcement": the
// per-tenant policy (mode + assurance-age) and the decision logic step 9
// of the auth middleware pipeline evaluates on every Authenticated
// request. This package only implements the policy loading and the
// evaluation logic itself. SavePolicy is its write side, used by the
// tenant settings API (PATCH /admin/settings).
package enforce

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/tenantconfig"
	"github.com/rs/zerolog/log"
)

// Mode is one of the three enforcement modes auth-internals.md §8's own
// table names.
type Mode string

const (
	// ModeOptional is the default: users may enroll but are never forced
	// to.
	ModeOptional Mode = "optional"
	// ModeRequired forces every user in the tenant to enroll.
	ModeRequired Mode = "required"
	// ModeRequiredForRoles forces enrollment only for users holding one
	// of Policy.RequiredRoles.
	ModeRequiredForRoles Mode = "required_for_roles"
)

// DefaultMaxAssuranceAge is auth-internals.md §8's own documented
// default for mfa_max_assurance_age.
const DefaultMaxAssuranceAge = 24 * time.Hour

// Policy is one tenant's resolved MFA enforcement configuration.
type Policy struct {
	Mode Mode
	// RequiredRoles is only consulted when Mode == ModeRequiredForRoles.
	RequiredRoles   []string
	MaxAssuranceAge time.Duration
}

// Applies reports whether this policy requires MFA for a user holding
// userRoles — auth-internals.md §8's own mode table: always for
// ModeRequired, only if userRoles intersects RequiredRoles for
// ModeRequiredForRoles, never for ModeOptional.
func (p Policy) Applies(userRoles []string) bool {
	switch p.Mode {
	case ModeRequired:
		return true
	case ModeRequiredForRoles:
		for _, role := range userRoles {
			if slices.Contains(p.RequiredRoles, role) {
				return true
			}
		}
		return false
	default:
		return false
	}
}

// tenantconfig key names this package owns — auth-internals.md §8
// doesn't name a storage key for any of these; nothing else in the
// tenantconfig-storage ticket (goerp#299) or its own doc section reserves
// this namespace, so this package picks and owns it, the same way #298
// invented the X-Client-Type header and #304 invented the mfa_token
// verify request's device_id field for a real, necessary detail no doc
// spelled out.
const (
	keyMode            = "mfa.enforcement_mode"
	keyRequiredRoles   = "mfa.required_roles"
	keyMaxAssuranceAge = "mfa.max_assurance_age_hours"
)

// Store loads a tenant's MFA enforcement Policy from per-tenant config
// storage (goerp#299).
type Store struct {
	config *tenantconfig.Store
}

func NewStore(config *tenantconfig.Store) *Store {
	return &Store{config: config}
}

// LoadPolicy resolves tenantID's current Policy. Every field defaults to
// its documented default when the tenant hasn't set it — ModeOptional,
// no required roles, DefaultMaxAssuranceAge — rather than erroring, since
// an unconfigured tenant is the common case (§8's own mode table marks
// "optional" as "Default"), not a misconfiguration.
func (s *Store) LoadPolicy(ctx context.Context, tenantID string) (Policy, error) {
	policy := Policy{Mode: ModeOptional, MaxAssuranceAge: DefaultMaxAssuranceAge}

	if v, ok, err := s.config.Get(ctx, tenantID, keyMode); err != nil {
		return Policy{}, fmt.Errorf("load mfa enforcement mode: %w", err)
	} else if ok {
		if ValidMode(Mode(v)) {
			policy.Mode = Mode(v)
		} else {
			log.Warn().Str("tenant_id", tenantID).Str("value", v).Msg("enforce: unrecognized mfa enforcement mode, defaulting to optional")
		}
	}

	if v, ok, err := s.config.Get(ctx, tenantID, keyMaxAssuranceAge); err != nil {
		return Policy{}, fmt.Errorf("load mfa max assurance age: %w", err)
	} else if ok {
		hours, err := strconv.Atoi(v)
		if err != nil || hours <= 0 {
			log.Warn().Str("tenant_id", tenantID).Str("value", v).Msg("enforce: unparseable mfa max assurance age, defaulting to 24h")
		} else {
			policy.MaxAssuranceAge = time.Duration(hours) * time.Hour
		}
	}

	if policy.Mode == ModeRequiredForRoles {
		if v, ok, err := s.config.Get(ctx, tenantID, keyRequiredRoles); err != nil {
			return Policy{}, fmt.Errorf("load mfa required roles: %w", err)
		} else if ok && v != "" {
			for role := range strings.SplitSeq(v, ",") {
				if role = strings.TrimSpace(role); role != "" {
					policy.RequiredRoles = append(policy.RequiredRoles, role)
				}
			}
		}
	}

	return policy, nil
}

// ValidMode reports whether mode is one of the three enforcement modes.
func ValidMode(mode Mode) bool {
	switch mode {
	case ModeOptional, ModeRequired, ModeRequiredForRoles:
		return true
	}
	return false
}

// SavePolicy writes every field of policy for tenantID in one
// transaction, the write side of LoadPolicy. It reads straight from
// tenantconfig with no cache in between, so the next LoadPolicy (and
// authcheck.Checker.EnforceMFA with it) sees the new policy immediately.
// A MaxAssuranceAge is stored in whole hours, rounded up, and
// RequiredRoles only under ModeRequiredForRoles, the one mode that reads
// them.
func (s *Store) SavePolicy(ctx context.Context, tenantID string, policy Policy) error {
	if !ValidMode(policy.Mode) {
		return fmt.Errorf("unknown mfa enforcement mode %q", policy.Mode)
	}
	if policy.MaxAssuranceAge <= 0 {
		return fmt.Errorf("mfa max assurance age must be positive, got %s", policy.MaxAssuranceAge)
	}
	hours := int64((policy.MaxAssuranceAge + time.Hour - 1) / time.Hour)
	var roles string
	if policy.Mode == ModeRequiredForRoles {
		roles = strings.Join(policy.RequiredRoles, ",")
	}
	if err := s.config.SetMany(ctx, tenantID, map[string]string{
		keyMode:            string(policy.Mode),
		keyRequiredRoles:   roles,
		keyMaxAssuranceAge: strconv.FormatInt(hours, 10),
	}); err != nil {
		return fmt.Errorf("save mfa enforcement policy: %w", err)
	}
	return nil
}
