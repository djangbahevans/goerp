package tenant

import (
	"errors"
	"testing"
	"uuid"
)

func TestProfile_UpdateMergesAndClears(t *testing.T) {
	store, conn := openTestStore(t)
	ctx := t.Context()
	tt := createTenant(t, store, conn, uniqueSlug(t), "Profile Co")

	got, err := store.UpdateProfile(ctx, tt.ID, ProfileUpdate{
		Name: new("Profile Co Ltd"), Address: new("1 Liberation Rd, Accra"), Website: new("https://example.com"),
		Country: new("GH"), DefaultCurrency: new("GHS"),
	})
	if err != nil {
		t.Fatalf("UpdateProfile() error: %v", err)
	}
	if got.Name != "Profile Co Ltd" || *got.Address != "1 Liberation Rd, Accra" || *got.Website != "https://example.com" || *got.Country != "GH" || *got.DefaultCurrency != "GHS" {
		t.Fatalf("UpdateProfile() = %+v", got)
	}

	// A nil field is untouched; "" clears.
	got, err = store.UpdateProfile(ctx, tt.ID, ProfileUpdate{Website: new("")})
	if err != nil {
		t.Fatalf("UpdateProfile() error: %v", err)
	}
	if got.Website != nil || got.Address == nil || got.Name != "Profile Co Ltd" {
		t.Errorf("after clearing website, profile = %+v", got)
	}

	read, err := store.GetProfile(ctx, tt.ID)
	if err != nil {
		t.Fatalf("GetProfile() error: %v", err)
	}
	if read.Name != got.Name || read.Website != nil || *read.Country != "GH" {
		t.Errorf("GetProfile() = %+v, want %+v", read, got)
	}
}

func TestProfile_SetLogoURLReturnsThePreviousOne(t *testing.T) {
	store, conn := openTestStore(t)
	ctx := t.Context()
	tt := createTenant(t, store, conn, uniqueSlug(t), "Logo Co")

	if prev, err := store.SetLogoURL(ctx, tt.ID, "https://cdn.example/a.png"); err != nil || prev != nil {
		t.Fatalf("first SetLogoURL() = %v, %v, want nil, nil", prev, err)
	}
	prev, err := store.SetLogoURL(ctx, tt.ID, "https://cdn.example/b.png")
	if err != nil || prev == nil || *prev != "https://cdn.example/a.png" {
		t.Fatalf("second SetLogoURL() = %v, %v, want the first URL", prev, err)
	}
	p, err := store.GetProfile(ctx, tt.ID)
	if err != nil || p.LogoURL == nil || *p.LogoURL != "https://cdn.example/b.png" {
		t.Fatalf("GetProfile() = %+v, %v", p, err)
	}
}

func TestProfile_UnknownTenant(t *testing.T) {
	store, _ := openTestStore(t)
	id := uuid.New().String()
	if _, err := store.GetProfile(t.Context(), id); !errors.Is(err, ErrTenantNotFound) {
		t.Errorf("GetProfile() error = %v, want ErrTenantNotFound", err)
	}
	if _, err := store.UpdateProfile(t.Context(), id, ProfileUpdate{Name: new("x")}); !errors.Is(err, ErrTenantNotFound) {
		t.Errorf("UpdateProfile() error = %v, want ErrTenantNotFound", err)
	}
	if _, err := store.SetLogoURL(t.Context(), id, "x"); !errors.Is(err, ErrTenantNotFound) {
		t.Errorf("SetLogoURL() error = %v, want ErrTenantNotFound", err)
	}
}
