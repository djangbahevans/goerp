package l10n

import (
	"strings"
	"testing"
)

func TestValidLocale(t *testing.T) {
	for _, locale := range []string{"en", "fr", "ar", "fil", "fr-GH", "pt-BR", "zh-Hant", "zh-Hant-TW", "es-419"} {
		if !ValidLocale(locale) {
			t.Errorf("ValidLocale(%q) = false, want true", locale)
		}
	}
	for _, locale := range []string{"", "e", "english", "EN", "en-us", "en_US", "en-", "-en", "en-USA", "zh-hant", "../en", "en/fr", "en.json", "en\x00"} {
		if ValidLocale(locale) {
			t.Errorf("ValidLocale(%q) = true, want false", locale)
		}
	}
}

func TestValidateFrontendTranslation(t *testing.T) {
	if err := ValidateFrontendTranslation("fr", []byte(`{"actions.create":"Nouveau contact","count.contacts_other":"{count} contacts"}`)); err != nil {
		t.Errorf("valid file: %v", err)
	}
	if err := ValidateFrontendTranslation("en", []byte(`{}`)); err != nil {
		t.Errorf("empty object: %v", err)
	}
	for name, tc := range map[string]struct {
		locale, data, want string
	}{
		"bad locale":   {"english", `{}`, "not a BCP 47 locale"},
		"nested":       {"en", `{"fields":{"name":"Name"}}`, "flat JSON object of strings"},
		"number value": {"en", `{"count":3}`, "flat JSON object of strings"},
		"array":        {"en", `["a"]`, "flat JSON object of strings"},
		"null":         {"en", `null`, "not null"},
		"not JSON":     {"en", `not json`, "flat JSON object of strings"},
		"duplicate":    {"en", `{"a":"1","a":"2"}`, "flat JSON object of strings"},
	} {
		err := ValidateFrontendTranslation(tc.locale, []byte(tc.data))
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: error = %v, want one containing %q", name, err, tc.want)
		}
	}
}
