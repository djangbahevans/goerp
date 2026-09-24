package tenant

import (
	"errors"
	"strings"
)

// ErrSlugReserved reports a slug no tenant may be created under
// (multitenancy-internals.md §1 "Reserved slugs").
var ErrSlugReserved = errors.New("tenant slug is reserved")

// builtinReservedSlugs names hosts a tenant's default domain,
// {slug}.{platform domain}, would otherwise take over.
var builtinReservedSlugs = []string{
	// Hosts the platform itself serves: shared-domain login, object
	// storage (allowlisted in every tenant's CSP), module registry.
	"app", "storage", "registry",
	// Conventional service hostnames under a platform domain.
	"www", "api", "admin", "mail", "smtp", "imap", "cdn", "static", "assets",
	"status", "docs", "help", "support", "blog",
	// Names that would pass for the platform's own sign-in or billing.
	"auth", "login", "sso", "account", "accounts", "billing", "security",
}

func newReservedSet() map[string]struct{} {
	set := make(map[string]struct{}, len(builtinReservedSlugs))
	for _, slug := range builtinReservedSlugs {
		set[slug] = struct{}{}
	}
	return set
}

// AddReservedSlugs extends the built-in list with a deployment's own
// (GOERP_RESERVED_SLUGS). Call it at startup, before the store serves
// requests: the set isn't guarded for concurrent writes.
func (s *Store) AddReservedSlugs(slugs ...string) {
	for _, slug := range slugs {
		if slug = strings.ToLower(strings.TrimSpace(slug)); slug != "" {
			s.reserved[slug] = struct{}{}
		}
	}
}

// IsReserved reports whether no tenant may be created under slug.
func (s *Store) IsReserved(slug string) bool {
	_, ok := s.reserved[strings.ToLower(slug)]
	return ok
}
