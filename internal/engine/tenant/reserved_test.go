package tenant

import (
	"errors"
	"testing"
)

func TestIsReserved_BuiltinListIsCaseInsensitive(t *testing.T) {
	s := NewStore(nil)
	for slug, want := range map[string]bool{
		"app": true, "storage": true, "registry": true, "www": true, "login": true, "billing": true,
		"APP": true, "acme": false, "apps": false,
	} {
		if got := s.IsReserved(slug); got != want {
			t.Errorf("IsReserved(%q) = %v, want %v", slug, got, want)
		}
	}
}

func TestAddReservedSlugs_NormalizesAndSkipsBlanks(t *testing.T) {
	s := NewStore(nil)
	s.AddReservedSlugs(" Wiki ", "", "crm")

	for slug, want := range map[string]bool{"wiki": true, "crm": true, "": false, "acme": false} {
		if got := s.IsReserved(slug); got != want {
			t.Errorf("IsReserved(%q) = %v, want %v", slug, got, want)
		}
	}
}

func TestCreateTenantAndReserveSlug_RefuseReservedSlugsBeforeTouchingTheDatabase(t *testing.T) {
	s := NewStore(nil)
	s.AddReservedSlugs("wiki")

	if _, err := s.CreateTenant(t.Context(), "app", "App"); !errors.Is(err, ErrSlugReserved) {
		t.Errorf("CreateTenant(app) error = %v, want ErrSlugReserved", err)
	}
	if _, err := s.ReserveSlug(t.Context(), "00000000-0000-7000-8000-000000000001", "wiki", "Wiki"); !errors.Is(err, ErrSlugReserved) {
		t.Errorf("ReserveSlug(wiki) error = %v, want ErrSlugReserved", err)
	}
}
