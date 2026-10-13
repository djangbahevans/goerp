package translation

import (
	"testing"

	abi "github.com/djangbahevans/goerp/contract/abi/v1"
)

func TestTranslate(t *testing.T) {
	entries := map[string]map[string]string{
		"en": {
			"contacts.hello":           "Hello",
			"contacts.english":         "English",
			"contacts.empty":           "fallback",
			"contacts.format":          "{0}: {1} / {missing}",
			"contacts.named":           "{name}: {count}; {name}",
			"contacts.literal":         "{missing}",
			"contacts.orders_zero":     "No orders",
			"contacts.orders_one":      "One order",
			"contacts.orders_other":    "{count} orders",
			"contacts.items_other":     "{count} items",
			"sales.private":            "secret",
			"contacts.onlyenglish_one": "one in English",
		},
		"fr": {
			"contacts.hello":        "Bonjour",
			"contacts.empty":        "",
			"contacts.orders_other": "{count} commandes",
		},
		"fr-GH": {"contacts.hello": "Salut"},
		"zh":    {"contacts.hello": "你好"},
		"ar": {
			"contacts.orders_other": "Arabic other",
			"contacts.orders_two":   "Arabic two",
		},
	}
	lookup := func(locale, key string) (string, bool) {
		value, ok := entries[locale][key]
		return value, ok
	}

	for _, test := range []struct {
		name  string
		input abi.TranslationInput
		want  string
	}{
		{
			name: "exact",
			input: abi.TranslationInput{
				Key:    "hello",
				Locale: "fr-GH",
			},
			want: "Salut",
		},
		{
			name: "language",
			input: abi.TranslationInput{
				Key:    "hello",
				Locale: "fr-CA",
			},
			want: "Bonjour",
		},
		{
			name: "script and region",
			input: abi.TranslationInput{
				Key:    "hello",
				Locale: "zh-Hant-TW",
			},
			want: "你好",
		},
		{
			name: "english",
			input: abi.TranslationInput{
				Key:    "english",
				Locale: "fr-GH",
			},
			want: "English",
		},
		{
			name: "unknown locale",
			input: abi.TranslationInput{
				Key:    "hello",
				Locale: "xx",
			},
			want: "Hello",
		},
		{
			name: "missing",
			input: abi.TranslationInput{
				Key:    "missing.{0}",
				Locale: "fr",
				Params: []any{"changed"},
			},
			want: "missing.{0}",
		},
		{
			name: "empty",
			input: abi.TranslationInput{
				Key:    "empty",
				Locale: "fr",
			},
			want: "",
		},
		{
			name: "isolation",
			input: abi.TranslationInput{
				Key:    "sales.private",
				Locale: "en",
			},
			want: "sales.private",
		},
		{
			name: "positional",
			input: abi.TranslationInput{
				Key:    "format",
				Params: []any{"name", 12},
			},
			want: "name: 12 / {missing}",
		},
		{
			name: "named",
			input: abi.TranslationInput{
				Key:    "named",
				Params: map[string]any{"name": "Kofi", "count": 3},
			},
			want: "Kofi: 3; Kofi",
		},
		{
			name: "single pass",
			input: abi.TranslationInput{
				Key:    "named",
				Params: map[string]any{"name": "{count}", "count": 3},
			},
			want: "{count}: 3; {count}",
		},
		{
			name: "unknown placeholder",
			input: abi.TranslationInput{
				Key: "literal",
			},
			want: "{missing}",
		},
		{
			name: "zero",
			input: abi.TranslationInput{
				Key:   "orders",
				Count: new(int64(0)),
			},
			want: "No orders",
		},
		{
			name: "one",
			input: abi.TranslationInput{
				Key:   "orders",
				Count: new(int64(1)),
			},
			want: "One order",
		},
		{
			name: "other",
			input: abi.TranslationInput{
				Key:    "orders",
				Count:  new(int64(5)),
				Params: map[string]any{"count": 5},
			},
			want: "5 orders",
		},
		{
			name: "zero without form",
			input: abi.TranslationInput{
				Key:    "items",
				Count:  new(int64(0)),
				Params: map[string]any{"count": 0},
			},
			want: "0 items",
		},
		{
			name: "zero prefers locale",
			input: abi.TranslationInput{
				Key:    "orders",
				Locale: "fr-GH",
				Count:  new(int64(0)),
				Params: map[string]any{"count": 0},
			},
			want: "0 commandes",
		},
		{
			name: "one fallback",
			input: abi.TranslationInput{
				Key:    "onlyenglish",
				Locale: "fr",
				Count:  new(int64(1)),
			},
			want: "one in English",
		},
		{
			name: "two uses other",
			input: abi.TranslationInput{
				Key:    "orders",
				Locale: "ar",
				Count:  new(int64(2)),
			},
			want: "Arabic other",
		},
		{
			name: "negative uses other",
			input: abi.TranslationInput{
				Key:    "orders",
				Count:  new(int64(-1)),
				Params: map[string]any{"count": -1},
			},
			want: "-1 orders",
		},
		{
			name: "missing forms",
			input: abi.TranslationInput{
				Key:   "absent",
				Count: new(int64(1)),
			},
			want: "absent",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := Translate(lookup, "contacts", test.input); got != test.want {
				t.Fatalf("Translate = %q, want %q", got, test.want)
			}
		})
	}

	for _, moduleName := range []string{"contacts", ""} {
		if got := Translate(nil, moduleName, abi.TranslationInput{Key: "hello"}); got != "hello" {
			t.Fatalf("unavailable lookup = %q", got)
		}
	}
}

func TestInterpolate(t *testing.T) {
	for _, test := range []struct {
		text string
		want string
	}{
		{"{0} {0}", "value value"},
		{"{01} {-1} {2}", "{01} {-1} {2}"},
		{"unfinished {0", "unfinished {0"},
		{"plain text", "plain text"},
	} {
		if got := interpolate(test.text, []any{"value"}); got != test.want {
			t.Errorf("interpolate(%q) = %q, want %q", test.text, got, test.want)
		}
	}
}
