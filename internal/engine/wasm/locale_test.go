package wasm

import (
	"context"
	"errors"
	"testing"

	abiv1 "github.com/djangbahevans/goerp/contract/abi/v1"
)

func TestInvocationLocaleSnapshotAndOverride(t *testing.T) {
	r := &Runtime{}
	calls := 0
	r.SetLocaleResolver(func(context.Context, string, string) (abiv1.LocaleInfo, error) {
		calls++
		return abiv1.LocaleInfo{Locale: "fr", Timezone: "Africa/Accra", Currency: "GHS"}, nil
	})
	mc := &ModuleContext{TenantID: "tenant", UserID: "actor"}
	ctx, err := r.PrepareInvocationLocale(WithLocaleOverride(t.Context(), "ar-MA"), mc)
	if err != nil {
		t.Fatal(err)
	}
	want := abiv1.LocaleInfo{Locale: "ar-MA", Timezone: "Africa/Accra", Currency: "GHS", Direction: "rtl"}
	if mc.Locale != want {
		t.Fatalf("locale = %+v, want %+v", mc.Locale, want)
	}
	if _, err := r.PrepareInvocationLocale(t.Context(), mc); err != nil {
		t.Fatal(err)
	}
	nested := &ModuleContext{TenantID: "tenant", UserID: "actor"}
	if _, err := r.PrepareInvocationLocale(ctx, nested); err != nil {
		t.Fatal(err)
	}
	if nested.Locale != want || calls != 1 {
		t.Fatalf("nested locale = %+v, resolver calls = %d", nested.Locale, calls)
	}

	mc.SetLocale(abiv1.LocaleInfo{Locale: "he", Timezone: "UTC", Currency: "ILS", Direction: "rtl"})
	if _, err := r.PrepareInvocationLocale(ctx, mc); err != nil {
		t.Fatal(err)
	}
	if mc.Locale.Locale != "he" || calls != 1 {
		t.Fatal("hook override replaced by resolution")
	}

	for _, other := range []*ModuleContext{
		{TenantID: "other-tenant", UserID: "actor"},
		{TenantID: "tenant", UserID: ""},
	} {
		before := calls
		if _, err := r.PrepareInvocationLocale(ctx, other); err != nil {
			t.Fatal(err)
		}
		if calls != before+1 || other.Locale.Locale != "fr" {
			t.Fatal("locale snapshot crossed an identity boundary")
		}
	}
}

func TestInvocationLocaleResolutionFailure(t *testing.T) {
	failure := errors.New("database unavailable")
	r := &Runtime{}
	r.SetLocaleResolver(func(context.Context, string, string) (abiv1.LocaleInfo, error) { return abiv1.LocaleInfo{}, failure })
	mc := &ModuleContext{}
	if _, err := r.PrepareInvocationLocale(t.Context(), mc); !errors.Is(err, failure) {
		t.Fatalf("error = %v", err)
	}
	if mc.localeResolved {
		t.Fatal("failed resolution cached as success")
	}
}
