//go:build !wasip1

package i18n

func hostI18nGetLocale(ptr, size uint32) uint64 {
	panic("sdk/go/i18n: host.i18n.get_locale is only available in a wasip1 build")
}
