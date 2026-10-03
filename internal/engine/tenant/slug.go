package tenant

import "regexp"

// MaxSlugLength leaves seven bytes for the tenant_ schema and role prefix
// within PostgreSQL's 63-byte identifier limit. Slugs contain only ASCII.
const MaxSlugLength = 56

const SlugPattern = `^[a-z][a-z0-9\-]{1,54}[a-z0-9]$`

var slugPattern = regexp.MustCompile(SlugPattern)

func ValidSlug(slug string) bool {
	return slugPattern.MatchString(slug)
}
