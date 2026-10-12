//go:build wasip1

package i18n

//go:wasmimport host.i18n get_locale
func hostI18nGetLocale(ptr, size uint32) uint64
