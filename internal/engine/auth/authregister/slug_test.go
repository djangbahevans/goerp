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
		{strings.Repeat("a", 56), strings.Repeat("a", 56)},
		{strings.Repeat("a", 57), strings.Repeat("a", 56)},
		{"3" + strings.Repeat("a", 100), "co-3" + strings.Repeat("a", 52)},
		{strings.Repeat("abc-", 30), strings.TrimRight(strings.Repeat("abc-", 14), "-")},
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

func TestValidSlug_LengthBoundary(t *testing.T) {
	for _, length := range []int{2, 3, 55, 56, 57, 64} {
		slug := strings.Repeat("a", length)

		if got, want := validSlug(slug), length >= 3 && length <= 56; got != want {
			t.Errorf("validSlug(%d characters) = %v, want %v", length, got, want)
		}
	}
}
