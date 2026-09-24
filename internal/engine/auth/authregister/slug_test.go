package authregister

import (
	"strings"
	"testing"
)

func TestDeriveSlug(t *testing.T) {
	cases := []struct{ name, want string }{
		{"Acme Corp", "acme-corp"},
		{"  Acme   Corp  ", "acme-corp"},
		{"Café Ghana Ltd.", "cafe-ghana-ltd"},
		{"O'Brien & Sons", "o-brien-sons"},
		{"3M Ghana", "co-3m-ghana"},
		{"--Acme--", "acme"},
		{"安全", ""},
		{"AB", "ab"},
		{strings.Repeat("abc-", 30), strings.TrimRight(strings.Repeat("abc-", 16), "-")},
	}
	for _, c := range cases {
		if got := DeriveSlug(c.name); got != c.want {
			t.Errorf("DeriveSlug(%q) = %q, want %q", c.name, got, c.want)
		}
	}
}

func TestDeriveSlug_ValidityFollowsTheSlugRule(t *testing.T) {
	for name, want := range map[string]bool{
		"Acme Corp":                true,
		"3M Ghana":                 true,
		"AB":                       false,
		"安全":                       false,
		strings.Repeat("abc-", 30): true,
	} {
		if got := validSlug(DeriveSlug(name)); got != want {
			t.Errorf("validSlug(DeriveSlug(%q)) = %v, want %v", name, got, want)
		}
	}
}
