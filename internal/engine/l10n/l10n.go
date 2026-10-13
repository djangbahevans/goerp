// Package l10n defines locale and translation-file rules shared by the engine and CLI.
package l10n

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"time"
)

const (
	FrontendTranslationsDir = "frontend/translations"
	BackendTranslationsDir  = "translations"
)

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

// ValidTimezone accepts an IANA zone name. time.LoadLocation also accepts
// "" and "Local", which name no zone a browser can render.
func ValidTimezone(tz string) bool {
	if tz == "" || tz == "Local" {
		return false
	}
	_, err := time.LoadLocation(tz)
	return err == nil
}

func ValidateFrontendTranslation(locale string, data []byte) error {
	_, err := decodeTranslation(FrontendTranslationsDir, locale, data)
	return err
}

func DecodeBackendTranslations(files map[string][]byte) (map[string]map[string]string, error) {
	if len(files) == 0 {
		return nil, nil
	}
	if _, ok := files["en"]; !ok {
		return nil, fmt.Errorf("translations/en.json: required when backend translations are present")
	}
	result := make(map[string]map[string]string, len(files))
	for _, locale := range slices.Sorted(maps.Keys(files)) {
		entries, err := decodeTranslation(BackendTranslationsDir, locale, files[locale])
		if err != nil {
			return nil, err
		}
		result[locale] = entries
	}
	return result, nil
}

func decodeTranslation(dir, locale string, data []byte) (map[string]string, error) {
	path := dir + "/" + locale + ".json"
	if !ValidLocale(locale) {
		return nil, fmt.Errorf("%s: %q is not a BCP 47 locale such as en or pt-BR", path, locale)
	}
	var raw map[string]jsontext.Value
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("%s: must be a flat JSON object of strings: %w", path, err)
	}
	if raw == nil {
		return nil, fmt.Errorf("%s: must be a flat JSON object of strings, not null", path)
	}
	entries := make(map[string]string, len(raw))
	for key, value := range raw {
		if value.Kind() != '"' {
			return nil, fmt.Errorf("%s: must be a flat JSON object of strings: key %q is not a string", path, key)
		}
		var text string
		if err := json.Unmarshal(value, &text); err != nil {
			return nil, fmt.Errorf("%s: decode key %q: %w", path, key, err)
		}
		entries[key] = text
	}
	return entries, nil
}
