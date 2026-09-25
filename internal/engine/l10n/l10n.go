// Package l10n holds the translation-file rules shared by the engine and
// the goerp CLI: the locale format and the layout and format of a module's
// frontend translation files (l10n-guide.md §2, §3, §10). It has no engine
// dependencies, so goerp module build can check a package the same way the
// engine does when it loads one.
package l10n

import (
	"encoding/json/v2"
	"fmt"
	"regexp"
)

// FrontendTranslationsDir is where a module's frontend translations live,
// both in its source tree and in its .erp package, apart from the backend
// translations/ directory.
const FrontendTranslationsDir = "frontend/translations"

// The locale and timezone a tenant with no locale settings of its own uses
// (l10n-guide.md §2 "Tenant default locale").
const (
	PlatformDefaultLocale   = "en"
	PlatformDefaultTimezone = "UTC"
)

// localePattern is a BCP 47 tag as l10n-guide.md §2 "Locale format" uses
// them: a language, an optional script and an optional region ("en",
// "pt-BR", "zh-Hant-TW", "es-419"). It also keeps a locale safe to put in
// a storage key or URL path.
var localePattern = regexp.MustCompile(`^[a-z]{2,3}(-[A-Z][a-z]{3})?(-([A-Z]{2}|[0-9]{3}))?$`)

func ValidLocale(locale string) bool {
	return localePattern.MatchString(locale)
}

// ValidateFrontendTranslation checks one frontend/translations/{locale}.json
// file: a valid locale name and a flat JSON object of string values
// (l10n-guide.md §3 "File structure").
func ValidateFrontendTranslation(locale string, data []byte) error {
	if !ValidLocale(locale) {
		return fmt.Errorf("%s/%s.json: %q is not a BCP 47 locale such as en or pt-BR", FrontendTranslationsDir, locale, locale)
	}
	var entries map[string]string
	if err := json.Unmarshal(data, &entries); err != nil {
		return fmt.Errorf("%s/%s.json: must be a flat JSON object of strings: %w", FrontendTranslationsDir, locale, err)
	}
	if entries == nil {
		return fmt.Errorf("%s/%s.json: must be a flat JSON object of strings, not null", FrontendTranslationsDir, locale)
	}
	return nil
}
