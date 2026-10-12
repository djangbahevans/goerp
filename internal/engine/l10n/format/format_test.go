package format

import (
	"math"
	"testing"
	"time"

	abi "github.com/djangbahevans/goerp/contract/abi/v1"
)

func TestNumber(t *testing.T) {
	for _, test := range []struct {
		name  string
		input abi.FormatNumberInput
		want  string
	}{
		{"English", abi.FormatNumberInput{Value: 1234567.89, Locale: "en"}, "1,234,567.89"},
		{"French", abi.FormatNumberInput{Value: 1234567.89, Locale: "fr"}, "1\u202f234\u202f567,89"},
		{"ungrouped", abi.FormatNumberInput{Value: 1234567.89, Locale: "fr", UseGrouping: new(false)}, "1234567,89"},
		{"fixed precision", abi.FormatNumberInput{Value: 1234.5, Locale: "en", DecimalPlaces: new(2)}, "1,234.50"},
		{"zero precision", abi.FormatNumberInput{Value: 1234.5, Locale: "en", DecimalPlaces: new(0)}, "1,235"},
		{"compact", abi.FormatNumberInput{Value: 1200.0, Locale: "en", Notation: "compact"}, "1.2K"},
		{"French compact", abi.FormatNumberInput{Value: 1200.0, Locale: "fr", Notation: "compact"}, "1,2\u00a0k"},
		{"scientific", abi.FormatNumberInput{Value: 1234.0, Locale: "en", Notation: "scientific", DecimalPlaces: new(2)}, "1.23E3"},
		{"percent", abi.FormatNumberInput{Value: 0.856, Locale: "en", Style: "percent"}, "85.6%"},
		{"French percent", abi.FormatNumberInput{Value: 0.856, Locale: "fr", Style: "percent"}, "85,6\u00a0%"},
		{"large integer", abi.FormatNumberInput{Value: int64(math.MaxInt64), Locale: "en"}, "9,223,372,036,854,775,807"},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := Number(test.input)
			if err != nil || got != test.want {
				t.Fatalf("Number = %q, %v; want %q", got, err, test.want)
			}
		})
	}
}

func TestCurrency(t *testing.T) {
	for _, test := range []struct {
		name  string
		input abi.FormatCurrencyInput
		want  string
	}{
		{"GHS", abi.FormatCurrencyInput{AmountMinor: 10000, CurrencyCode: "GHS", Locale: "en-GH"}, "GHS\u00a0100.00"},
		{"USD", abi.FormatCurrencyInput{AmountMinor: 10000, CurrencyCode: "USD", Locale: "en"}, "$100.00"},
		{"XOF", abi.FormatCurrencyInput{AmountMinor: 10000, CurrencyCode: "XOF", Locale: "en"}, "F\u202fCFA\u00a010,000"},
		{"KWD", abi.FormatCurrencyInput{AmountMinor: 10000, CurrencyCode: "KWD", Locale: "en", Opts: abi.CurrencyFormatOptions{ShowSymbol: new(false)}}, "10.000"},
		{"French", abi.FormatCurrencyInput{AmountMinor: 10000, CurrencyCode: "USD", Locale: "fr"}, "100,00\u00a0$US"},
		{"French GHS", abi.FormatCurrencyInput{AmountMinor: 10000, CurrencyCode: "GHS", Locale: "fr"}, "100,00\u00a0GHS"},
		{"French XOF", abi.FormatCurrencyInput{AmountMinor: 10000, CurrencyCode: "XOF", Locale: "fr"}, "10\u202f000\u00a0F\u202fCFA"},
		{"KWD symbol", abi.FormatCurrencyInput{AmountMinor: 10000, CurrencyCode: "KWD", Locale: "en"}, "KWD\u00a010.000"},
		{"after", abi.FormatCurrencyInput{AmountMinor: 10000, CurrencyCode: "USD", Locale: "en", Opts: abi.CurrencyFormatOptions{SymbolPosition: "after"}}, "100.00 $"},
		{"before", abi.FormatCurrencyInput{AmountMinor: -10000, CurrencyCode: "USD", Locale: "fr", Opts: abi.CurrencyFormatOptions{SymbolPosition: "before"}}, "$US -100,00"},
		{"minimum integer", abi.FormatCurrencyInput{AmountMinor: math.MinInt64, CurrencyCode: "USD", Locale: "en", Opts: abi.CurrencyFormatOptions{ShowSymbol: new(false)}}, "-92,233,720,368,547,758.08"},
		{"one minor unit", abi.FormatCurrencyInput{AmountMinor: 1, CurrencyCode: "KWD", Locale: "en", Opts: abi.CurrencyFormatOptions{ShowSymbol: new(false)}}, "0.001"},
		{"new currency", abi.FormatCurrencyInput{AmountMinor: 10000, CurrencyCode: "XCG", Locale: "en", Opts: abi.CurrencyFormatOptions{ShowSymbol: new(false)}}, "100.00"},
		{"fund precision", abi.FormatCurrencyInput{AmountMinor: 10000, CurrencyCode: "UYW", Locale: "en", Opts: abi.CurrencyFormatOptions{ShowSymbol: new(false)}}, "1.0000"},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := Currency(test.input)
			if err != nil || got != test.want {
				t.Fatalf("Currency = %q, %v; want %q", got, err, test.want)
			}
		})
	}
}

func TestDate(t *testing.T) {
	instant := time.Date(2026, time.May, 16, 0, 30, 5, 123000000, time.UTC)
	for _, test := range []struct {
		locale      string
		format      string
		zone        string
		includeTime bool
		want        string
	}{
		{"en", "short", "UTC", false, "5/16/26"},
		{"en", "medium", "UTC", false, "May 16, 2026"},
		{"en", "long", "UTC", false, "May 16, 2026"},
		{"en", "full", "UTC", false, "Saturday, May 16, 2026"},
		{"fr", "short", "UTC", false, "16/05/2026"},
		{"fr", "medium", "UTC", false, "16 mai 2026"},
		{"fr", "long", "UTC", false, "16 mai 2026"},
		{"fr", "full", "UTC", false, "samedi 16 mai 2026"},
		{"fr", "dd MMM yyyy", "UTC", false, "16 mai 2026"},
		{"en", "MMMM yyyy", "UTC", false, "May 2026"},
		{"ru", "MMMM LLLL", "UTC", false, "мая май"},
		{"en", "yyyy-MM-dd HH:mm:ss.SSS XXX", "America/New_York", false, "2026-05-15 20:30:05.123 -04:00"},
		{"en", "yyyy-MM-dd 'at' h:mm a ''", "UTC", false, "2026-05-16 at 12:30 AM '"},
		{"fr", "medium", "UTC", true, "16 mai 2026, 00:30:05"},
	} {
		t.Run(test.locale+test.format+test.zone, func(t *testing.T) {
			got, err := Date(abi.FormatDateInput{UnixMS: instant.UnixMilli(), Format: test.format, Locale: test.locale, Timezone: test.zone, IncludeTime: test.includeTime})
			if err != nil || got != test.want {
				t.Fatalf("Date = %q, %v; want %q", got, err, test.want)
			}
		})
	}
}

func TestInvalidFormats(t *testing.T) {
	for _, input := range []abi.FormatNumberInput{
		{Value: "1", Locale: "en"}, {Value: math.NaN(), Locale: "en"},
		{Value: 1.0, Locale: "en", DecimalPlaces: new(-1)},
		{Value: 1.0, Locale: "en", DecimalPlaces: new(101)},
		{Value: 1.0, Locale: "en", Notation: "engineering"},
		{Value: 1.0, Locale: "en", Style: "currency"},
		{Value: 1.0, Locale: "invalid_locale"},
	} {
		if _, err := Number(input); err == nil {
			t.Errorf("Number accepted %+v", input)
		}
	}
	for _, code := range []string{"ZZZ", "XXX", "XAU", "usd", ""} {
		if _, err := Currency(abi.FormatCurrencyInput{CurrencyCode: code, Locale: "en"}); err == nil {
			t.Errorf("Currency accepted %q", code)
		}
	}
	for _, input := range []abi.FormatDateInput{
		{Locale: "en", Timezone: "Invalid/Zone"}, {Locale: "en", Timezone: "Local"},
		{Locale: "en", Timezone: "UTC", Format: "yyyy '"},
		{Locale: "en", Timezone: "UTC", Format: "QQQ"},
		{Locale: "en", Timezone: "UTC", Format: "EEEEEE"},
		{Locale: "en", Timezone: "UTC", Format: "aaaaa"},
	} {
		if _, err := Date(input); err == nil {
			t.Errorf("Date accepted %+v", input)
		}
	}
}
