package password

import "time"

// Outcome is a sign-in password check's verdict (auth-internals.md §3
// "Password policy at sign-in").
type Outcome string

const (
	// UpdateRecommended leaves the session normal and asks the member to
	// change their password.
	UpdateRecommended Outcome = "recommended"
	// ChangeRequired restricts the session until the password is changed.
	ChangeRequired Outcome = "required"
)

// Result is a sign-in password check's result, carried with the login
// through a tenant pick, a shared-domain handoff and MFA until the
// session is issued. The zero value means the password passed.
type Result struct {
	Outcome Outcome `json:"outcome,omitzero"`
	// Deadline is when a recommended change becomes required; set only
	// under the tenant's "require" enforcement.
	Deadline *time.Time `json:"deadline,omitzero"`
}

// CheckSignIn checks plain against p and the always-on checks.
// joinedAt is when the member joined the tenant (zero when unknown).
//
// A password that breaks a platform rule is only ever a nudge: no tenant
// chose that rule, so no tenant's grace period covers it. One that only
// falls short of p's raised minimum is required to change under
// "require" enforcement, from ChangedAt + GraceDays, or at once for a
// member who joined after ChangedAt.
func (p TenantPolicy) CheckSignIn(plain, email string, joinedAt, now time.Time) Result {
	if Global.Validate(plain, email) != nil {
		return Result{Outcome: UpdateRecommended}
	}
	if WithMinLength(p.MinLength).Validate(plain, email) == nil {
		return Result{}
	}
	// A "require" setting always stamps ChangedAt; without one there is no
	// grace period to count from, so only nudge.
	if p.Enforcement != EnforcementRequire || p.ChangedAt == nil {
		return Result{Outcome: UpdateRecommended}
	}
	if joinedAt.After(*p.ChangedAt) {
		return Result{Outcome: ChangeRequired}
	}
	deadline := p.ChangedAt.AddDate(0, 0, p.GraceDays)
	if now.Before(deadline) {
		return Result{Outcome: UpdateRecommended, Deadline: &deadline}
	}
	return Result{Outcome: ChangeRequired}
}
