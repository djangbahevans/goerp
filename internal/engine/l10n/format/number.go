// Package format renders invocation values with CLDR locale conventions.
package format

import (
	"cmp"
	"fmt"
	"math"
	"strconv"
	"strings"
	"unicode"

	"github.com/agentable/go-intl/locale"
	"github.com/agentable/go-intl/numberformat"
	abi "github.com/djangbahevans/goerp/contract/abi/v1"
)

func Number(input abi.FormatNumberInput) (string, error) {
	locales, err := locale.ParseList(input.Locale)
	if err != nil {
		return "", err
	}

	var value numberformat.Value
	switch n := input.Value.(type) {
	case int64:
		value = numberformat.Int(n)
	case uint64:
		value = numberformat.Uint(n)
	case int:
		value = numberformat.Int(int64(n))
	case uint:
		value = numberformat.Uint(uint64(n))
	case int8:
		value = numberformat.Int(int64(n))
	case int16:
		value = numberformat.Int(int64(n))
	case int32:
		value = numberformat.Int(int64(n))
	case uint8:
		value = numberformat.Uint(uint64(n))
	case uint16:
		value = numberformat.Uint(uint64(n))
	case uint32:
		value = numberformat.Uint(uint64(n))
	case float32:
		input.Value = float64(n)
		return Number(input)
	case float64:
		if math.IsNaN(n) || math.IsInf(n, 0) {
			return "", fmt.Errorf("value must be finite")
		}
		value = numberformat.Float(n)
	default:
		return "", fmt.Errorf("value must be a number")
	}

	style := cmp.Or(input.Style, "decimal")
	if style != "decimal" && style != "percent" {
		return "", fmt.Errorf("unsupported number style %q", style)
	}
	notation := cmp.Or(input.Notation, "standard")
	if notation != "standard" && notation != "compact" && notation != "scientific" {
		return "", fmt.Errorf("unsupported notation %q", notation)
	}
	if input.DecimalPlaces != nil && (*input.DecimalPlaces < 0 || *input.DecimalPlaces > 100) {
		return "", fmt.Errorf("decimal_places must be between 0 and 100")
	}
	grouping := "always"
	if input.UseGrouping != nil && !*input.UseGrouping {
		grouping = "false"
	}
	options := numberformat.Options{Style: new(style), Notation: new(notation), UseGrouping: new(grouping)}
	if input.DecimalPlaces != nil {
		options.MinimumFractionDigits = input.DecimalPlaces
		options.MaximumFractionDigits = input.DecimalPlaces
	} else if style == "percent" {
		options.MaximumFractionDigits = new(3)
	}
	formatter, err := numberformat.New(locales, options)
	if err != nil {
		return "", err
	}

	return formatter.Format(value), nil
}

func Currency(input abi.FormatCurrencyInput) (string, error) {
	scale, known := currencyMinorUnits[input.CurrencyCode]
	if !known {
		return "", fmt.Errorf("unknown ISO 4217 currency %q", input.CurrencyCode)
	}
	if scale < 0 {
		return "", fmt.Errorf("currency %q has no ISO 4217 minor unit", input.CurrencyCode)
	}
	position := cmp.Or(input.Opts.SymbolPosition, "locale_default")
	if position != "locale_default" && position != "before" && position != "after" {
		return "", fmt.Errorf("unsupported symbol_position %q", position)
	}
	locales, err := locale.ParseList(input.Locale)
	if err != nil {
		return "", err
	}
	// Decimal text preserves every minor unit, including amounts beyond float64's exact integer range.
	decimal := strconv.FormatInt(input.AmountMinor, 10)
	negative := strings.HasPrefix(decimal, "-")
	decimal = strings.TrimPrefix(decimal, "-")
	if len(decimal) <= scale {
		decimal = strings.Repeat("0", scale-len(decimal)+1) + decimal
	}
	if scale > 0 {
		decimal = decimal[:len(decimal)-scale] + "." + decimal[len(decimal)-scale:]
	}
	if negative {
		decimal = "-" + decimal
	}
	value, err := numberformat.Decimal(decimal)
	if err != nil {
		return "", err
	}
	options := numberformat.Options{
		Style:                 new("currency"),
		Currency:              new(input.CurrencyCode),
		MinimumFractionDigits: new(scale),
		MaximumFractionDigits: new(scale),
	}
	show := input.Opts.ShowSymbol == nil || *input.Opts.ShowSymbol
	if !show {
		options.Style = new("decimal")
	}
	formatter, err := numberformat.New(locales, options)
	if err != nil {
		return "", err
	}
	if !show || position == "locale_default" {
		return formatter.Format(value), nil
	}

	var body, symbol strings.Builder
	for _, part := range formatter.FormatToParts(value) {
		switch part.Type {
		case "currency":
			symbol.WriteString(part.Value)
		case "literal":
			if strings.TrimSpace(part.Value) != "" {
				body.WriteString(part.Value)
			}
		default:
			body.WriteString(part.Value)
		}
	}
	if position == "after" {
		return body.String() + " " + symbol.String(), nil
	}
	separator := ""
	if strings.ContainsFunc(symbol.String(), unicode.IsLetter) {
		separator = " "
	}
	return symbol.String() + separator + body.String(), nil
}
