package enforce

import (
	"strings"
	"time"
)

// Decision is step 9's outcome — the empty string for "allowed", or one
// of the three documented 403 error codes.
type Decision string

const (
	Allowed Decision = ""
	// SetupRequired: the policy requires MFA for this user and they have
	// no enrolled factor at all.
	SetupRequired Decision = "mfa_setup_required"
	// FactorRequired: the user is enrolled, but this session's own amr
	// never included an MFA factor — e.g. the tenant's policy tightened
	// from optional to required after this session was already issued.
	FactorRequired Decision = "mfa_required"
	// ReverifyRequired: this session's MFA assurance has aged past the
	// policy's MaxAssuranceAge.
	ReverifyRequired Decision = "mfa_reverify_required"
)

// Context is what Evaluate needs to know about the current user/session
// — deliberately caller-supplied rather than this package querying
// mfa.Store/sessions itself, so Evaluate stays a pure function callers
// can unit test without a database.
type Context struct {
	// UserRoles is the requesting user's current live roles, checked
	// against Policy.RequiredRoles for ModeRequiredForRoles.
	UserRoles []string
	// Enrolled reports whether the user has any active MFA factor at
	// all (mfa.Store.ListActiveByUser non-empty).
	Enrolled bool
	// AMRHasFactor reports whether this session's own amr claim already
	// includes an MFA method — distinct from Enrolled, see FactorRequired.
	AMRHasFactor bool
	// MFAVerifiedAt is this session's sessions.mfa_verified_at, nil if
	// never set.
	MFAVerifiedAt *time.Time
}

// Evaluate implements auth-internals.md §8's step 9 decision exactly:
//
//	Load tenant MFA policy (mode + mfa_max_assurance_age)
//	If required and not enrolled: 403 mfa_setup_required
//	If policy requires MFA for this user:
//	    If amr has no MFA factor: 403 mfa_required
//	    Else if NOW() - mfa_verified_at > mfa_max_assurance_age: 403 mfa_reverify_required
func Evaluate(policy Policy, ctx Context, now time.Time) Decision {
	if !policy.Applies(ctx.UserRoles) {
		return Allowed
	}
	if !ctx.Enrolled {
		return SetupRequired
	}
	if !ctx.AMRHasFactor {
		return FactorRequired
	}
	if ctx.MFAVerifiedAt == nil || now.Sub(*ctx.MFAVerifiedAt) > policy.MaxAssuranceAge {
		return ReverifyRequired
	}
	return Allowed
}

// exemptRoutes are the fixed-path routes auth-internals.md §8 exempts
// from this check outright — they either carry no full session yet
// (enroll*/verify) or are the handler that resolves ReverifyRequired
// itself (reverify), so rejecting the request before it reaches them
// would make each unreachable exactly when it's needed.
var exemptRoutes = map[string]bool{
	"/auth/mfa/verify":   true,
	"/auth/mfa/reverify": true,
}

// RouteExempt reports whether path is exempt from MFA enforcement —
// auth-internals.md §8's /auth/mfa/enroll*, /auth/mfa/verify,
// /auth/mfa/reverify.
func RouteExempt(path string) bool {
	if exemptRoutes[path] {
		return true
	}
	return strings.HasPrefix(path, "/auth/mfa/enroll")
}
