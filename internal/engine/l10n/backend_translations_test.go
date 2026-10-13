package l10n

import (
	"strings"
	"testing"
)

func TestDecodeBackendTranslations(t *testing.T) {
	for name, tc := range map[string]struct {
		files map[string][]byte
		want  string
	}{
		"none":         {},
		"valid":        {files: map[string][]byte{"en": []byte(`{"label":"","a.b":"Name"}`), "pt-BR": []byte(`{"a.b":"Nome"}`)}},
		"missing base": {files: map[string][]byte{"fr": []byte(`{}`)}, want: "translations/en.json"},
		"bad locale":   {files: map[string][]byte{"en": []byte(`{}`), "english": []byte(`{}`)}, want: "translations/english.json"},
		"nested":       {files: map[string][]byte{"en": []byte(`{"a":{"b":"Name"}}`)}, want: "flat JSON object of strings"},
		"null object":  {files: map[string][]byte{"en": []byte(`null`)}, want: "not null"},
		"null value":   {files: map[string][]byte{"en": []byte(`{"a":null}`)}, want: "flat JSON object of strings"},
		"number":       {files: map[string][]byte{"en": []byte(`{"a":2}`)}, want: "flat JSON object of strings"},
		"array":        {files: map[string][]byte{"en": []byte(`[]`)}, want: "flat JSON object of strings"},
		"duplicate":    {files: map[string][]byte{"en": []byte(`{"a":"1","a":"2"}`)}, want: "flat JSON object of strings"},
	} {
		t.Run(name, func(t *testing.T) {
			got, err := DecodeBackendTranslations(tc.files)
			if tc.want != "" {
				if err == nil || !strings.Contains(err.Error(), tc.want) {
					t.Fatalf("error = %v, want %q", err, tc.want)
				}
				return
			}

			if err != nil {
				t.Fatal(err)
			}

			if name == "valid" && (got["en"]["a.b"] != "Name" || got["pt-BR"]["a.b"] != "Nome") {
				t.Fatalf("translations = %v", got)
			}
		})
	}
}
