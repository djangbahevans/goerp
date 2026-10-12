package modeltest

import (
	"testing"

	abi "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/djangbahevans/goerp/internal/engine/tenant"
	"github.com/djangbahevans/goerp/internal/engine/tenantconfig"
	"github.com/djangbahevans/goerp/internal/engine/user"
	fixture "github.com/djangbahevans/goerp/sdk/go/modeltest/testdata/localefixture"
)

func assertRequestLocale(t *testing.T, response *Response, want abi.LocaleInfo) {
	t.Helper()
	if response.StatusCode != 200 {
		t.Fatalf("response = %d: %v", response.StatusCode, response.JSON("error"))
	}
	for key, value := range map[string]string{"locale": want.Locale, "timezone": want.Timezone, "currency": want.Currency, "direction": want.Direction} {
		if got := response.JSON(key); got != value {
			t.Errorf("%s = %v, want %q", key, got, value)
		}
	}
}

func TestHarnessInvocationLocale(t *testing.T) {
	t.Chdir("testdata/localefixture")
	h := NewHarness(t)
	want := abi.LocaleInfo{Locale: "en", Timezone: "UTC", Direction: "ltr"}
	assertRequestLocale(t, h.GET("/localeprobe/locale"), want)
	config := tenantconfig.NewStore(h.DB.db)
	for key, value := range map[string]string{"l10n.default_locale": "fr", "l10n.default_timezone": "Africa/Accra"} {
		if err := config.Set(t.Context(), h.TenantID, key, value); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := h.tenants.UpdateProfile(t.Context(), h.TenantID, tenant.ProfileUpdate{DefaultCurrency: new("GHS")}); err != nil {
		t.Fatal(err)
	}
	want = abi.LocaleInfo{Locale: "fr", Timezone: "Africa/Accra", Currency: "GHS", Direction: "ltr"}
	assertRequestLocale(t, h.GET("/localeprobe/locale"), want)
	assertRequestLocale(t, h.Anonymous().GET("/localeprobe/locale"), want)
	if err := h.RunJob(fixture.Process, want); err != nil {
		t.Fatal(err)
	}
	if err := h.Deliver(fixture.Received, want); err != nil {
		t.Fatal(err)
	}

	users := user.NewStore(h.DB.db)
	if _, err := users.UpdateProfile(t.Context(), h.UserID, user.ProfileUpdate{
		Locale:   user.NullableField{Set: true, Value: new("ar-MA")},
		Timezone: user.NullableField{Set: true, Value: new("Africa/Casablanca")},
	}); err != nil {
		t.Fatal(err)
	}
	want.Locale, want.Timezone, want.Direction = "ar-MA", "Africa/Casablanca", "rtl"
	assertRequestLocale(t, h.GET("/localeprobe/locale"), want)
	if err := h.RunJob(fixture.Process, want); err != nil {
		t.Fatal(err)
	}
	if err := h.Deliver(fixture.Received, want); err != nil {
		t.Fatal(err)
	}

	overridden := want
	overridden.Locale, overridden.Direction = "fr-GH", "ltr"
	assertRequestLocale(t, h.WithLocale("fr-GH").WithHeader("Accept-Language", "ar").GET("/localeprobe/locale"), overridden)
	assertRequestLocale(t, h.GET("/localeprobe/locale"), want)
}

func TestHarnessWithLocale(t *testing.T) {
	t.Chdir("testdata/localefixture")
	h := NewHarness(t, WithLocale("he"))
	want := abi.LocaleInfo{Locale: "he", Timezone: "UTC", Direction: "rtl"}
	assertRequestLocale(t, h.GET("/localeprobe/locale"), want)
	if err := h.RunJob(fixture.Process, want); err != nil {
		t.Fatal(err)
	}
	if err := h.Deliver(fixture.Received, want); err != nil {
		t.Fatal(err)
	}
}
