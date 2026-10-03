package authregister

import (
	"regexp"
	"strings"
	"unicode"

	"github.com/djangbahevans/goerp/internal/engine/tenant"

	"golang.org/x/text/unicode/norm"
)

var slugPattern = regexp.MustCompile(tenant.SlugPattern)

const maxSlugLength = tenant.MaxSlugLength

func validSlug(slug string) bool {
	return slugPattern.MatchString(slug)
}

// DeriveSlug turns a company name into a tenant slug candidate: accents
// stripped, lowercased, every run of other characters collapsed to one
// hyphen, trimmed to 56 characters. A name starting with a digit gets a
// "co-" prefix, since a slug must start with a letter. The result can
// still be invalid (too short); callers check with validSlug.
func DeriveSlug(companyName string) string {
	var b strings.Builder
	pendingHyphen := false

	for _, r := range norm.NFKD.String(companyName) {
		switch {
		case unicode.Is(unicode.Mn, r):
			// Combining marks left by NFKD ("é" → "e" + U+0301).
		case r < unicode.MaxASCII && (unicode.IsLetter(r) || unicode.IsDigit(r)):
			if pendingHyphen && b.Len() > 0 {
				b.WriteByte('-')
			}
			pendingHyphen = false
			b.WriteRune(unicode.ToLower(r))
		default:
			pendingHyphen = true
		}
	}

	slug := b.String()
	if slug != "" && slug[0] >= '0' && slug[0] <= '9' {
		slug = "co-" + slug
	}

	if len(slug) > maxSlugLength {
		slug = strings.TrimRight(slug[:maxSlugLength], "-")
	}

	return slug
}
