//go:build wasip1

package i18n

//go:wasmimport host.i18n get_locale
func hostI18nGetLocale(ptr, size uint32) uint64

//go:wasmimport host.i18n format_number
func hostI18nFormatNumber(ptr, size uint32) uint64

//go:wasmimport host.i18n format_currency
func hostI18nFormatCurrency(ptr, size uint32) uint64

//go:wasmimport host.i18n format_date
func hostI18nFormatDate(ptr, size uint32) uint64
