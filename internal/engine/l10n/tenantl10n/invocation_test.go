package tenantl10n

import (
	"context"
	"errors"
	"testing"

	abiv1 "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/djangbahevans/goerp/internal/engine/tenant"
	"github.com/djangbahevans/goerp/internal/engine/user"
)

type invocationUsers struct {
	profile *user.Profile
	err     error
	calls   int
}

func (s *invocationUsers) GetProfile(context.Context, string) (*user.Profile, error) {
	s.calls++
	return s.profile, s.err
}

type invocationTenants struct {
	profile *tenant.Profile
	err     error
}

func (s invocationTenants) GetProfile(context.Context, string) (*tenant.Profile, error) {
	return s.profile, s.err
}

func TestInvocationResolverFallbacks(t *testing.T) {
	for _, tc := range []struct {
		name       string
		config     fakeConfig
		profile    *user.Profile
		profileErr error
		userID     string
		currency   *string
		want       abiv1.LocaleInfo
	}{
		{
			name:       "platform",
			profileErr: user.ErrProfileNotFound,
			userID:     "user",
			want:       abiv1.LocaleInfo{Locale: "en", Timezone: "UTC", Direction: "ltr"},
		},
		{
			name:     "tenant",
			config:   fakeConfig{KeyDefaultLocale: "fr", KeyDefaultTimezone: "Africa/Accra"},
			profile:  &user.Profile{},
			userID:   "user",
			currency: new("GHS"),
			want:     abiv1.LocaleInfo{Locale: "fr", Timezone: "Africa/Accra", Currency: "GHS", Direction: "ltr"},
		},
		{
			name:     "user",
			config:   fakeConfig{KeyDefaultLocale: "fr", KeyDefaultTimezone: "Africa/Accra"},
			profile:  &user.Profile{Locale: new("ar-MA"), Timezone: new("Africa/Casablanca")},
			userID:   "user",
			currency: new("GHS"),
			want:     abiv1.LocaleInfo{Locale: "ar-MA", Timezone: "Africa/Casablanca", Currency: "GHS", Direction: "rtl"},
		},
		{
			name:    "independent preferences",
			config:  fakeConfig{KeyDefaultTimezone: "Africa/Accra"},
			profile: &user.Profile{Locale: new("fr-GH")},
			userID:  "user",
			want:    abiv1.LocaleInfo{Locale: "fr-GH", Timezone: "Africa/Accra", Direction: "ltr"},
		},
		{
			name:     "no user",
			config:   fakeConfig{KeyDefaultLocale: "ar"},
			currency: new("MAD"),
			want:     abiv1.LocaleInfo{Locale: "ar", Timezone: "UTC", Currency: "MAD", Direction: "rtl"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			users := &invocationUsers{profile: tc.profile, err: tc.profileErr}
			resolver := &InvocationResolver{
				Settings: NewStore(tc.config, []string{"en", "fr", "ar"}),
				Users:    users,
				Tenants:  invocationTenants{profile: &tenant.Profile{DefaultCurrency: tc.currency}},
			}
			got, err := resolver.Resolve(t.Context(), "tenant", tc.userID)
			if err != nil || got != tc.want {
				t.Fatalf("Resolve = %+v, %v, want %+v", got, err, tc.want)
			}
			if tc.userID == "" && users.calls != 0 {
				t.Fatal("anonymous invocation reads a user profile")
			}
		})
	}
}

func TestInvocationResolverPreservesErrors(t *testing.T) {
	failure := errors.New("database unavailable")
	users := &invocationUsers{err: failure}
	resolver := &InvocationResolver{
		Settings: NewStore(fakeConfig{}, []string{"en"}),
		Users:    users,
		Tenants:  invocationTenants{profile: &tenant.Profile{}},
	}
	if _, err := resolver.Resolve(t.Context(), "tenant", "user"); !errors.Is(err, failure) {
		t.Fatalf("profile error = %v", err)
	}
	resolver.Tenants = invocationTenants{err: failure}
	if _, err := resolver.Resolve(t.Context(), "tenant", ""); !errors.Is(err, failure) {
		t.Fatalf("currency error = %v", err)
	}
}
