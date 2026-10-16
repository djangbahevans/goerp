package wasm

import (
	"context"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	abiv1 "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/djangbahevans/goerp/internal/engine/wasm/wasmtest"
	"github.com/vmihailenco/msgpack/v5"
)

func TestHostI18nFormatters(t *testing.T) {
	ctx := t.Context()
	r := newHostDBTestRuntime(t, nil, 1)
	compiled, err := r.wazero.CompileModule(ctx, buildHostCallerModule("host.i18n", []string{"format_number", "format_currency", "format_date"}))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = compiled.Close(context.Background()) })
	inst, err := newModuleInstance(ctx, "i18n-caller", compiled, r)
	if err != nil {
		t.Fatal(err)
	}
	resolutionCalls := 0
	r.SetLocaleResolver(func(context.Context, string, string) (abiv1.LocaleInfo, error) {
		resolutionCalls++
		return abiv1.LocaleInfo{Locale: "fr", Timezone: "America/New_York"}, nil
	})
	mc := &ModuleContext{}
	inst.SetModuleContext(mc)
	r.RegisterInstance(inst)
	t.Cleanup(func() { r.UnregisterInstance(inst) })

	instant := time.Date(2026, time.May, 16, 0, 30, 0, 0, time.UTC).UnixMilli()
	for _, test := range []struct {
		function string
		input    any
		want     string
	}{
		{"format_number", abiv1.FormatNumberInput{Value: 1234.5}, "1\u202f234,5"},
		{"format_number", abiv1.FormatNumberInput{Value: int64(math.MaxInt64), Locale: "en"}, "9,223,372,036,854,775,807"},
		{"format_number", abiv1.FormatNumberInput{Value: 1234.5, UseGrouping: new(false)}, "1234,5"},
		{"format_currency", abiv1.FormatCurrencyInput{AmountMinor: 10000, CurrencyCode: "USD"}, "100,00\u00a0$US"},
		{"format_date", abiv1.FormatDateInput{UnixMS: instant, Format: "yyyy-MM-dd HH:mm"}, "2026-05-15 20:30"},
		{"format_date", abiv1.FormatDateInput{UnixMS: instant, Format: "yyyy-MM-dd HH:mm", Timezone: "Africa/Accra"}, "2026-05-16 00:30"},
	} {
		envelope := callHost(t, ctx, inst, "call_"+test.function, test.input)
		var got string
		if !envelope.OK {
			t.Fatalf("%s: %v", test.function, envelope.Error)
		}
		if err := msgpack.Unmarshal(envelope.Data, &got); err != nil || got != test.want {
			t.Fatalf("%s = %q, %v; want %q", test.function, got, err, test.want)
		}
	}
	for _, test := range []struct {
		function string
		input    any
	}{
		{"format_currency", map[string]any{"amount_minor": 1.5, "currency_code": "USD"}},
		{"format_currency", map[string]any{"amount_minor": nil, "currency_code": "USD"}},
		{"format_currency", map[string]any{"currency_code": "USD"}},
		{"format_currency", map[string]any{"amount_minor": uint64(math.MaxUint64), "currency_code": "USD"}},
		{"format_currency", abiv1.FormatCurrencyInput{CurrencyCode: "ZZZ"}},
		{"format_number", abiv1.FormatNumberInput{Value: 1.0, DecimalPlaces: new(-1)}},
		{"format_number", map[string]any{"value": "1234"}},
		{"format_date", abiv1.FormatDateInput{Timezone: "Invalid/Zone"}},
		{"format_date", abiv1.FormatDateInput{Format: "invalid"}},
	} {
		envelope := callHost(t, ctx, inst, "call_"+test.function, test.input)
		if envelope.OK || envelope.Error == nil || envelope.Error.Code != abiv1.ErrCodeDeserializeError {
			t.Fatalf("%s accepted invalid input: %+v", test.function, envelope)
		}
	}
	if resolutionCalls != 1 {
		t.Fatalf("locale resolution calls = %d, want one invocation snapshot", resolutionCalls)
	}
}

func TestI18nSDKFormatters(t *testing.T) {
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

	wasmtest.Precompile(t, data, wasmtest.SharedCompilationCacheDir())

	r := newHostcallTestRuntime(t, nil, 1)
	mc := &ModuleContext{}
	mc.SetLocale(abiv1.LocaleInfo{Locale: "fr", Timezone: "America/New_York"})
	result := callHostcallFixture(t, t.Context(), r, data, mc, "check_formats")
	if !result.OK {
		t.Fatal(result.Error)
	}
}
