package i18n

import (
	"time"

	abi "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/djangbahevans/goerp/sdk/go/internal/hostcall"
)

type formatOptions struct {
	number   abi.FormatNumberInput
	currency abi.CurrencyFormatOptions
	date     abi.FormatDateInput
}

type FormatOption func(*formatOptions)
type DateOption = FormatOption
type CurrencyOption = FormatOption

// Locale selects an explicit BCP 47 locale for a formatter.
func Locale(locale string) FormatOption {
	return func(o *formatOptions) { o.number.Locale, o.date.Locale = locale, locale }
}

// DecimalPlaces fixes the number of fractional digits between 0 and 100.
func DecimalPlaces(n int) FormatOption {
	return func(o *formatOptions) { o.number.DecimalPlaces = new(n) }
}

// UseGrouping controls number grouping; grouping is enabled by default.
func UseGrouping(enabled bool) FormatOption {
	return func(o *formatOptions) { o.number.UseGrouping = new(enabled) }
}

// Notation selects standard, compact or scientific number notation.
func Notation(notation string) FormatOption {
	return func(o *formatOptions) { o.number.Notation = notation }
}

// ShowSymbol controls currency symbol visibility; symbols are shown by default.
func ShowSymbol(show bool) CurrencyOption {
	return func(o *formatOptions) { o.currency.ShowSymbol = new(show) }
}

type Position string

const (
	Before        Position = "before"
	After         Position = "after"
	LocaleDefault Position = "locale_default"
)

// SymbolPosition selects currency placement; the default follows the locale.
func SymbolPosition(position Position) CurrencyOption {
	return func(o *formatOptions) { o.currency.SymbolPosition = string(position) }
}

// Timezone selects an explicit IANA timezone for date and time formatting.
func Timezone(timezone string) DateOption {
	return func(o *formatOptions) { o.date.Timezone = timezone }
}

var (
	Short  DateOption = dateStyle("short")
	Medium DateOption = dateStyle("medium")
	Long   DateOption = dateStyle("long")
	Full   DateOption = dateStyle("full")
)

func dateStyle(style string) DateOption {
	return func(o *formatOptions) { o.date.Format = style }
}

func options(opts []FormatOption) formatOptions {
	var result formatOptions
	for _, opt := range opts {
		opt(&result)
	}
	return result
}

func formatted(invoke hostcall.Invoke, input any) string {
	var result string
	if err := hostcall.Do(invoke, input, &result); err != nil {
		panic(err)
	}
	return result
}

// FormatNumber formats a finite number using the invocation locale unless overridden.
// It panics on invalid options or a host error.
func FormatNumber(n float64, opts ...FormatOption) string {
	input := options(opts).number
	input.Value = n
	return formatted(hostI18nFormatNumber, input)
}

// FormatInt formats an integer without losing precision, using the invocation locale.
// It panics on invalid options or a host error.
func FormatInt(n int64, opts ...FormatOption) string {
	input := options(opts).number
	input.Value = n
	return formatted(hostI18nFormatNumber, input)
}

// FormatPercent formats a ratio as a percentage, with up to three fractional digits.
// It panics on invalid options or a host error.
func FormatPercent(n float64, opts ...FormatOption) string {
	input := options(opts).number
	input.Value, input.Style = n, "percent"
	return formatted(hostI18nFormatNumber, input)
}

// FormatCurrency formats integer minor units with the currency's ISO 4217 precision.
// It uses the invocation locale and panics on an unknown currency or host error.
func FormatCurrency(amountMinor int64, currencyCode string, opts ...CurrencyOption) string {
	o := options(opts)

	return formatted(hostI18nFormatCurrency, abi.FormatCurrencyInput{
		AmountMinor:  amountMinor,
		CurrencyCode: currencyCode,
		Locale:       o.number.Locale,
		Opts:         o.currency,
	})
}

// FormatCurrencyLocale formats integer minor units using an explicit locale.
// It panics on invalid options, an unknown currency or a host error.
func FormatCurrencyLocale(amountMinor int64, currencyCode, locale string, opts ...CurrencyOption) string {
	o := options(opts)

	return formatted(hostI18nFormatCurrency, abi.FormatCurrencyInput{
		AmountMinor:  amountMinor,
		CurrencyCode: currencyCode,
		Locale:       locale,
		Opts:         o.currency,
	})
}

// FormatDate formats a date in the invocation timezone and locale; Medium is the default.
// It panics on invalid options or a host error.
func FormatDate(t time.Time, opts ...DateOption) string {
	input := options(opts).date
	input.UnixMS = t.UnixMilli()
	return formatted(hostI18nFormatDate, input)
}

// FormatDatePattern formats a date with a CLDR pattern in the invocation timezone.
// Supported fields are y, M, L, d, E, G, a, H, h, K, k, m, s, S, z, X and x.
// It panics on an unsupported pattern, invalid options or a host error.
func FormatDatePattern(t time.Time, pattern string, opts ...DateOption) string {
	input := options(opts).date
	input.UnixMS, input.Format = t.UnixMilli(), pattern
	return formatted(hostI18nFormatDate, input)
}

// FormatDateTime formats a date and time using the invocation locale and timezone.
// Medium is the default. It panics on invalid options or a host error.
func FormatDateTime(t time.Time, opts ...DateOption) string {
	input := options(opts).date
	input.UnixMS, input.IncludeTime = t.UnixMilli(), true
	return formatted(hostI18nFormatDate, input)
}

// FormatDateTimeInTZ formats a date and time in an explicit IANA timezone.
// It panics on an invalid timezone, invalid options or a host error.
func FormatDateTimeInTZ(t time.Time, timezone string, opts ...DateOption) string {
	input := options(opts).date
	input.UnixMS = t.UnixMilli()
	input.Timezone = timezone
	input.IncludeTime = true

	return formatted(hostI18nFormatDate, input)
}
