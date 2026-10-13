// Command i18nfixture exercises SDK translations and formatting across the WASM host boundary.
package main

import (
	"fmt"
	"math"
	"time"

	"github.com/djangbahevans/goerp/sdk/go/engine"
	"github.com/djangbahevans/goerp/sdk/go/i18n"
	"github.com/vmihailenco/msgpack/v5"
)

//go:wasmexport check_formats
func checkFormats() uint64 {
	t := time.Date(2026, time.May, 16, 0, 30, 0, 0, time.UTC)
	checks := [][2]string{
		{i18n.FormatNumber(1234.5), "1\u202f234,5"},
		{i18n.FormatNumber(1234.5, i18n.Locale("en"), i18n.DecimalPlaces(2), i18n.UseGrouping(false)), "1234.50"},
		{i18n.FormatNumber(1200, i18n.Locale("en"), i18n.Notation("compact")), "1.2K"},
		{i18n.FormatInt(math.MaxInt64, i18n.Locale("en")), "9,223,372,036,854,775,807"},
		{i18n.FormatPercent(0.856), "85,6\u00a0%"},
		{i18n.FormatCurrency(10000, "USD"), "100,00\u00a0$US"},
		{i18n.FormatCurrencyLocale(10000, "USD", "en", i18n.SymbolPosition(i18n.After)), "100.00 $"},
		{i18n.FormatCurrencyLocale(10000, "KWD", "en", i18n.ShowSymbol(false)), "10.000"},
		{i18n.FormatDate(t, i18n.Short), "15/05/2026"},
		{i18n.FormatDate(t, i18n.Locale("en"), i18n.Full, i18n.Timezone("UTC")), "Saturday, May 16, 2026"},
		{i18n.FormatDatePattern(t, "yyyy-MM-dd HH:mm"), "2026-05-15 20:30"},
		{i18n.FormatDateTime(t), "15 mai 2026, 20:30:00"},
		{i18n.FormatDateTimeInTZ(t, "Africa/Accra"), "16 mai 2026, 00:30:00"},
	}
	for _, check := range checks {
		if check[0] != check[1] {
			return writeResult(map[string]any{"ok": false, "error": fmt.Sprintf("got %q, want %q", check[0], check[1])})
		}
	}
	return writeResult(map[string]any{"ok": true})
}

//go:wasmexport check_translations
func checkTranslations() uint64 {
	checks := [][2]string{
		{i18n.T("hello", "Kofi"), "Bonjour Kofi"},
		{i18n.TLocale("hello", "en", "Kofi"), "Hello Kofi"},
		{i18n.TParams("named", map[string]any{"name": "Kofi"}), "Bonjour Kofi"},
		{i18n.T("named", map[string]any{"name": "Kofi"}), "Bonjour Kofi"},
		{i18n.TLocale("named", "fr", map[string]any{"name": "Kofi"}), "Bonjour Kofi"},
		{i18n.TPlural("orders", 0, nil), "No orders"},
		{i18n.TPlural("orders", 1, nil), "One order"},
		{i18n.TPlural("orders", 5, map[string]any{"count": 5}), "5 orders"},
		{i18n.TPlural("items", 0, map[string]any{"count": 0}), "0 items"},
		{i18n.TPlural("missing", 1, nil), "missing"},
		{i18n.T("sales.private"), "sales.private"},
		{i18n.T("missing.{0}", "value"), "missing.{0}"},
		{i18n.T("hello", make(chan int)), "hello"},
	}
	for _, check := range checks {
		if check[0] != check[1] {
			return writeResult(map[string]any{"ok": false, "error": fmt.Sprintf("got %q, want %q", check[0], check[1])})
		}
	}
	return writeResult(map[string]any{"ok": true})
}

func writeResult(value any) uint64 {
	data, err := msgpack.Marshal(value)
	if err != nil {
		panic(err)
	}
	ptr := engine.Allocate(uint32(len(data)))
	engine.WriteMem(ptr, data)
	return uint64(ptr)<<32 | uint64(len(data))
}

//go:wasmexport allocate
func allocate(size uint32) uint32 { return engine.Allocate(size) }

//go:wasmexport deallocate
func deallocate(ptr, size uint32) { engine.Deallocate(ptr, size) }

func main() {}
