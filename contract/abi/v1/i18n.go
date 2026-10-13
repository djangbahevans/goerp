package abi

// LocaleInfo holds the resolved presentation settings for one invocation.
// Currency is empty when the tenant has no default currency.
type LocaleInfo struct {
	Locale    string `msgpack:"locale"`
	Timezone  string `msgpack:"timezone"`
	Currency  string `msgpack:"currency"`
	Direction string `msgpack:"direction"`
}

// TranslationInput uses a key relative to the calling module. Count selects
// _zero, _one or _other; nil performs an unsuffixed lookup.
type TranslationInput struct {
	Key    string `msgpack:"key"`
	Locale string `msgpack:"locale,omitempty"`
	Params any    `msgpack:"params,omitempty"`
	Count  *int64 `msgpack:"count,omitempty"`
}

type FormatNumberInput struct {
	Value         any    `msgpack:"value"`
	Locale        string `msgpack:"locale,omitempty"`
	DecimalPlaces *int   `msgpack:"decimal_places,omitempty"`
	UseGrouping   *bool  `msgpack:"use_grouping,omitempty"`
	Notation      string `msgpack:"notation,omitempty"`
	Style         string `msgpack:"style,omitempty"`
}

type CurrencyFormatOptions struct {
	SymbolPosition string `msgpack:"symbol_position,omitempty"`
	ShowSymbol     *bool  `msgpack:"show_symbol,omitempty"`
}

type FormatCurrencyInput struct {
	AmountMinor  int64                 `msgpack:"amount_minor"`
	CurrencyCode string                `msgpack:"currency_code"`
	Locale       string                `msgpack:"locale,omitempty"`
	Opts         CurrencyFormatOptions `msgpack:"opts"`
}

type FormatDateInput struct {
	UnixMS      int64  `msgpack:"unix_ms"`
	Format      string `msgpack:"format,omitempty"`
	Locale      string `msgpack:"locale,omitempty"`
	Timezone    string `msgpack:"timezone,omitempty"`
	IncludeTime bool   `msgpack:"include_time,omitempty"`
}
