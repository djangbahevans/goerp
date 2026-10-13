// Package i18n translates module keys, reads invocation settings, and formats numbers, currencies and dates.
package i18n

import (
	abi "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/djangbahevans/goerp/sdk/go/internal/hostcall"
)

func current() abi.LocaleInfo {
	var info abi.LocaleInfo
	if err := hostcall.Do(hostI18nGetLocale, struct{}{}, &info); err != nil {
		panic(err)
	}

	return info
}

// CurrentLocale returns the BCP 47 locale for the current invocation.
// It panics if the host cannot supply invocation settings.
func CurrentLocale() string { return current().Locale }

// CurrentTimezone returns the IANA timezone for the current invocation.
// It panics if the host cannot supply invocation settings.
func CurrentTimezone() string { return current().Timezone }

// CurrentCurrency returns the tenant's default ISO 4217 currency, or an empty
// string when unset. It panics if the host cannot supply invocation settings.
func CurrentCurrency() string { return current().Currency }

// CurrentDirection returns "ltr" or "rtl" for the current invocation.
// It panics if the host cannot supply invocation settings.
func CurrentDirection() string { return current().Direction }
