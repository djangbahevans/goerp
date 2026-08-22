package enforce

import (
	"testing"
	"time"
)

func TestEvaluate_OptionalModeAlwaysAllowed(t *testing.T) {
	policy := Policy{Mode: ModeOptional, MaxAssuranceAge: DefaultMaxAssuranceAge}
	got := Evaluate(policy, Context{}, time.Now())
	if got != Allowed {
		t.Errorf("Evaluate() = %q, want Allowed", got)
	}
}

func TestEvaluate_RequiredModeNotEnrolledYieldsSetupRequired(t *testing.T) {
	policy := Policy{Mode: ModeRequired, MaxAssuranceAge: DefaultMaxAssuranceAge}
	got := Evaluate(policy, Context{Enrolled: false}, time.Now())
	if got != SetupRequired {
		t.Errorf("Evaluate() = %q, want SetupRequired", got)
	}
}

func TestEvaluate_EnrolledButAMRLacksFactorYieldsFactorRequired(t *testing.T) {
	policy := Policy{Mode: ModeRequired, MaxAssuranceAge: DefaultMaxAssuranceAge}
	got := Evaluate(policy, Context{Enrolled: true, AMRHasFactor: false}, time.Now())
	if got != FactorRequired {
		t.Errorf("Evaluate() = %q, want FactorRequired", got)
	}
}

func TestEvaluate_StaleAssuranceYieldsReverifyRequired(t *testing.T) {
	policy := Policy{Mode: ModeRequired, MaxAssuranceAge: time.Hour}
	verifiedAt := time.Now().Add(-2 * time.Hour)
	got := Evaluate(policy, Context{
		Enrolled:      true,
		AMRHasFactor:  true,
		MFAVerifiedAt: &verifiedAt,
	}, time.Now())
	if got != ReverifyRequired {
		t.Errorf("Evaluate() = %q, want ReverifyRequired", got)
	}
}

func TestEvaluate_NilMFAVerifiedAtYieldsReverifyRequired(t *testing.T) {
	policy := Policy{Mode: ModeRequired, MaxAssuranceAge: DefaultMaxAssuranceAge}
	got := Evaluate(policy, Context{Enrolled: true, AMRHasFactor: true, MFAVerifiedAt: nil}, time.Now())
	if got != ReverifyRequired {
		t.Errorf("Evaluate() = %q, want ReverifyRequired", got)
	}
}

func TestEvaluate_FreshAssuranceWithinWindowIsAllowed(t *testing.T) {
	policy := Policy{Mode: ModeRequired, MaxAssuranceAge: DefaultMaxAssuranceAge}
	verifiedAt := time.Now().Add(-time.Minute)
	got := Evaluate(policy, Context{
		Enrolled:      true,
		AMRHasFactor:  true,
		MFAVerifiedAt: &verifiedAt,
	}, time.Now())
	if got != Allowed {
		t.Errorf("Evaluate() = %q, want Allowed", got)
	}
}

func TestEvaluate_RequiredForRolesOnlyAppliesToMatchingRole(t *testing.T) {
	policy := Policy{Mode: ModeRequiredForRoles, RequiredRoles: []string{"admin"}, MaxAssuranceAge: DefaultMaxAssuranceAge}

	admin := Evaluate(policy, Context{UserRoles: []string{"admin"}, Enrolled: false}, time.Now())
	if admin != SetupRequired {
		t.Errorf("admin Evaluate() = %q, want SetupRequired", admin)
	}

	member := Evaluate(policy, Context{UserRoles: []string{"member"}, Enrolled: false}, time.Now())
	if member != Allowed {
		t.Errorf("non-admin Evaluate() = %q, want Allowed", member)
	}
}

func TestPolicy_AppliesModeOptionalNeverApplies(t *testing.T) {
	policy := Policy{Mode: ModeOptional}
	if policy.Applies([]string{"admin"}) {
		t.Error("Applies() = true for ModeOptional, want false")
	}
}

func TestPolicy_AppliesModeRequiredAlwaysApplies(t *testing.T) {
	policy := Policy{Mode: ModeRequired}
	if !policy.Applies(nil) {
		t.Error("Applies() = false for ModeRequired with no roles, want true")
	}
}

func TestRouteExempt(t *testing.T) {
	cases := []struct {
		path string
		want bool
	}{
		{"/auth/mfa/verify", true},
		{"/auth/mfa/reverify", true},
		{"/auth/mfa/enroll", true},
		{"/auth/mfa/enroll/totp", true},
		{"/auth/login", false},
		{"/contacts", false},
	}
	for _, c := range cases {
		if got := RouteExempt(c.path); got != c.want {
			t.Errorf("RouteExempt(%q) = %v, want %v", c.path, got, c.want)
		}
	}
}
