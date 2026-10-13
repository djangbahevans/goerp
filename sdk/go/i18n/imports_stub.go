//go:build !wasip1

package i18n

func hostI18nT(ptr, size uint32) uint64 {
	panic("sdk/go/i18n: host.i18n.t is only available in a wasip1 build")
}

func hostI18nGetLocale(ptr, size uint32) uint64 {
	panic("sdk/go/i18n: host.i18n.get_locale is only available in a wasip1 build")
}

func hostI18nFormatNumber(ptr, size uint32) uint64 {
	panic("sdk/go/i18n: host.i18n.format_number is only available in a wasip1 build")
}

func hostI18nFormatCurrency(ptr, size uint32) uint64 {
	panic("sdk/go/i18n: host.i18n.format_currency is only available in a wasip1 build")
}

func hostI18nFormatDate(ptr, size uint32) uint64 {
	panic("sdk/go/i18n: host.i18n.format_date is only available in a wasip1 build")
}
