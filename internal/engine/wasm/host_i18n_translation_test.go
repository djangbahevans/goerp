package wasm

import (
	"context"
	"errors"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	abiv1 "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/vmihailenco/msgpack/v5"
)

func TestHostI18nTranslation(t *testing.T) {
	ctx := t.Context()
	r := newHostDBTestRuntime(t, nil, 1)
	compiled, err := r.wazero.CompileModule(ctx, buildHostCallerModule("host.i18n", []string{"t"}))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = compiled.Close(context.Background()) })
	inst, err := newModuleInstance(ctx, "translation-caller", compiled, r)
	if err != nil {
		t.Fatal(err)
	}
	inst.SetModuleContext(&ModuleContext{ModuleName: "contacts"})
	r.RegisterInstance(inst)
	t.Cleanup(func() { r.UnregisterInstance(inst) })
	r.SetLocaleResolver(func(context.Context, string, string) (abiv1.LocaleInfo, error) {
		return abiv1.LocaleInfo{Locale: "fr-GH"}, nil
	})
	entries := map[string]map[string]string{
		"en": {
			"contacts.hello":        "Hello",
			"sales.private":         "secret",
			"contacts.orders_other": "{count} orders",
		},
		"fr": {
			"contacts.hello":       "Bonjour {0}",
			"contacts.orders_zero": "Aucune commande",
			"contacts.orders_one":  "Une commande",
		},
	}
	r.SetTranslationLookup(func(locale, key string) (string, bool) {
		value, ok := entries[locale][key]
		return value, ok
	})

	for _, test := range []struct {
		input abiv1.TranslationInput
		want  string
	}{
		{
			input: abiv1.TranslationInput{
				Key:    "hello",
				Params: []any{"Kofi"},
			},
			want: "Bonjour Kofi",
		},
		{
			input: abiv1.TranslationInput{
				Key:    "hello",
				Locale: "en",
			},
			want: "Hello",
		},
		{
			input: abiv1.TranslationInput{
				Key: "sales.private",
			},
			want: "sales.private",
		},
		{
			input: abiv1.TranslationInput{
				Key:   "orders",
				Count: new(int64(0)),
			},
			want: "Aucune commande",
		},
		{
			input: abiv1.TranslationInput{
				Key:   "orders",
				Count: new(int64(1)),
			},
			want: "Une commande",
		},
		{
			input: abiv1.TranslationInput{
				Key:    "orders",
				Count:  new(int64(5)),
				Params: map[string]any{"count": 5},
			},
			want: "5 orders",
		},
		{
			input: abiv1.TranslationInput{
				Key: "absent",
			},
			want: "absent",
		},
	} {
		envelope := callHost(t, ctx, inst, "call_t", test.input)
		var got string
		if !envelope.OK {
			t.Fatalf("translation failed: %+v", envelope.Error)
		}
		if err := msgpack.Unmarshal(envelope.Data, &got); err != nil || got != test.want {
			t.Fatalf("translation = %q, %v; want %q", got, err, test.want)
		}
	}

	r.SetTranslationLookup(nil)
	envelope := callHost(t, ctx, inst, "call_t", abiv1.TranslationInput{Key: "hello"})
	var got string
	if err := msgpack.Unmarshal(envelope.Data, &got); !envelope.OK || err != nil || got != "hello" {
		t.Fatalf("unavailable store = %q, %v: %+v", got, err, envelope)
	}
	r.SetLocaleResolver(func(context.Context, string, string) (abiv1.LocaleInfo, error) {
		return abiv1.LocaleInfo{}, errors.New("unavailable")
	})
	inst.SetModuleContext(&ModuleContext{ModuleName: "contacts"})
	envelope = callHost(t, ctx, inst, "call_t", abiv1.TranslationInput{Key: "hello"})
	if err := msgpack.Unmarshal(envelope.Data, &got); !envelope.OK || err != nil || got != "hello" {
		t.Fatalf("locale failure = %q, %v: %+v", got, err, envelope)
	}

	r.SetTranslationLookup(func(locale, key string) (string, bool) {
		value, ok := entries[locale][key]
		return value, ok
	})
	envelope = callHost(t, ctx, inst, "call_t", abiv1.TranslationInput{Key: "hello", Locale: "en"})
	if err := msgpack.Unmarshal(envelope.Data, &got); !envelope.OK || err != nil || got != "Hello" {
		t.Fatalf("explicit locale during resolution failure = %q, %v: %+v", got, err, envelope)
	}

	for _, count := range []any{1.5, uint64(math.MaxUint64), "1"} {
		envelope = callHost(t, ctx, inst, "call_t", map[string]any{"key": "orders", "count": count})
		if envelope.OK || envelope.Error == nil || envelope.Error.Code != abiv1.ErrCodeDeserializeError {
			t.Fatalf("accepted invalid count %v: %+v", count, envelope)
		}
	}
}

func TestI18nSDKTranslations(t *testing.T) {
	path := filepath.Join(t.TempDir(), "i18n.wasm")
	cmd := exec.CommandContext(t.Context(), "go", "build", "-buildmode=c-shared", "-o", path, "./testdata/i18nfixture")
	cmd.Env = append(os.Environ(), "GOOS=wasip1", "GOARCH=wasm")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build SDK fixture: %v\n%s", err, output)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	r := newHostcallTestRuntime(t, nil, 1)
	entries := map[string]map[string]string{
		"en": {
			"contacts.hello":        "Hello {0}",
			"contacts.orders_zero":  "No orders",
			"contacts.orders_one":   "One order",
			"contacts.orders_other": "{count} orders",
			"contacts.items_other":  "{count} items",
			"sales.private":         "secret",
		},
		"fr": {
			"contacts.hello": "Bonjour {0}",
			"contacts.named": "Bonjour {name}",
		},
	}
	r.SetTranslationLookup(func(locale, key string) (string, bool) {
		value, ok := entries[locale][key]
		return value, ok
	})
	mc := &ModuleContext{ModuleName: "contacts"}
	mc.SetLocale(abiv1.LocaleInfo{Locale: "fr-GH"})
	result := callHostcallFixture(t, t.Context(), r, data, mc, "check_translations")
	if !result.OK {
		t.Fatal(result.Error)
	}
}
