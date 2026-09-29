package password

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestPolicyValidate(t *testing.T) {
	cases := []struct {
		name     string
		policy   Policy
		password string
		email    string
		want     error
	}{
		{"accepts a long passphrase", Global, "correct horse battery staple", "kwame@example.com", nil},
		{"rejects 11 characters", Global, "abcdefghijk", "kwame@example.com", ErrTooShort},
		{"accepts exactly 12 characters", Global, "qzvkplmwxtrb", "kwame@example.com", nil},
		{"counts characters, not bytes", Global, strings.Repeat("é", 12), "kwame@example.com", nil},
		{"rejects over the maximum", Global, strings.Repeat("a", Global.MaxLength+1), "kwame@example.com", ErrTooLong},
		{"rejects the email username, any case", Global, "my-KWAME-password", "Kwame@example.com", ErrContainsEmail},
		{"ignores a very short email username", Global, "ab-long-passphrase", "ab@example.com", nil},
		{"rejects a common password, any case", Global, "Schmetterling", "kwame@example.com", ErrCommon},
		{"needs no composition", Global, "all lowercase words", "kwame@example.com", nil},
		{"applies a raised minimum", WithMinLength(16), "qzvkplmwxtrbqzv", "kwame@example.com", ErrTooShort},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.policy.Validate(tc.password, tc.email); !errors.Is(got, tc.want) {
				t.Errorf("Validate(%q, %q) = %v, want %v", tc.password, tc.email, got, tc.want)
			}
		})
	}
}

func TestWithMinLength_NeverBelowGlobal(t *testing.T) {
	if got := WithMinLength(8); got != Global {
		t.Errorf("WithMinLength(8) = %+v, want Global %+v", got, Global)
	}
	if got := WithMinLength(16); got.MinLength != 16 || got.MaxLength != Global.MaxLength {
		t.Errorf("WithMinLength(16) = %+v, want MinLength 16 and the global MaxLength", got)
	}
}

func TestCommonPasswords_OnlyHoldsEntriesLongEnoughToMatter(t *testing.T) {
	if len(commonPasswords) == 0 {
		t.Fatal("common password list is empty")
	}
	for p := range commonPasswords {
		if n := len([]rune(p)); n < Global.MinLength {
			t.Errorf("entry %q has %d characters, below Global.MinLength", p, n)
		}
		if p != strings.ToLower(p) {
			t.Errorf("entry %q isn't lowercased", p)
		}
	}
}

func TestTenantPolicyCheckSignIn(t *testing.T) {
	now := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	changed := now.AddDate(0, 0, -5)
	before := changed.AddDate(0, -1, 0)
	after := changed.Add(time.Hour)
	const email = "kwame@example.com"
	const short = "qzvkplmwxtrb" // 12 characters: meets Global, not 16

	nudge := TenantPolicy{MinLength: 16, Enforcement: EnforcementNudge, GraceDays: 14, ChangedAt: &changed}
	require := TenantPolicy{MinLength: 16, Enforcement: EnforcementRequire, GraceDays: 14, ChangedAt: &changed}
	expired := require
	expired.GraceDays = 3
	unstamped := require
	unstamped.ChangedAt = nil
	deadline := changed.AddDate(0, 0, 14)

	cases := []struct {
		name     string
		policy   TenantPolicy
		password string
		joinedAt time.Time
		want     Result
	}{
		{"passes the tenant's minimum", require, "qzvkplmwxtrbqzvk", before, Result{}},
		{"platform policy only", TenantPolicy{MinLength: 12, Enforcement: EnforcementRequire}, short, before, Result{}},
		{"short under nudge", nudge, short, before, Result{Outcome: UpdateRecommended}},
		{"short under require, within grace", require, short, before, Result{Outcome: UpdateRecommended, Deadline: &deadline}},
		{"short under require, grace over", expired, short, before, Result{Outcome: ChangeRequired}},
		{"short under require, joined after the change", require, short, after, Result{Outcome: ChangeRequired}},
		{"short under require, join time unknown", require, short, time.Time{}, Result{Outcome: UpdateRecommended, Deadline: &deadline}},
		{"short under require without a change time", unstamped, short, before, Result{Outcome: UpdateRecommended}},
		{"common password is only a nudge", expired, "Schmetterling", after, Result{Outcome: UpdateRecommended}},
		{"below the platform minimum is only a nudge", expired, "short-pw", after, Result{Outcome: UpdateRecommended}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.policy.CheckSignIn(tc.password, email, tc.joinedAt, now)
			if got.Outcome != tc.want.Outcome || !equalTimes(got.Deadline, tc.want.Deadline) {
				t.Errorf("CheckSignIn() = %+v (deadline %v), want %+v (deadline %v)", got, got.Deadline, tc.want, tc.want.Deadline)
			}
		})
	}
}

func equalTimes(a, b *time.Time) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.Equal(*b)
}
