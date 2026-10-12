package abi

// LocaleInfo holds the resolved presentation settings for one invocation.
// Currency is empty when the tenant has no default currency.
type LocaleInfo struct {
	Locale    string `msgpack:"locale"`
	Timezone  string `msgpack:"timezone"`
	Currency  string `msgpack:"currency"`
	Direction string `msgpack:"direction"`
}
