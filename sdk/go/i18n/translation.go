package i18n

import (
	abi "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/djangbahevans/goerp/sdk/go/internal/hostcall"
)

// T translates a module-relative key in the invocation's locale, falling back
// to its language and then English. Params replace {0}, {1}, ...; a single
// map[string]any replaces named placeholders. Missing keys and host errors return key.
func T(key string, params ...any) string {
	return translate(abi.TranslationInput{Key: key, Params: translationParams(params)})
}

// TParams translates a module-relative key with named {variable} substitutions.
// Missing keys and host errors return key; unknown placeholders remain unchanged.
func TParams(key string, params map[string]any) string {
	return translate(abi.TranslationInput{Key: key, Params: params})
}

// TLocale translates a module-relative key using an explicit locale, then its
// language and English. Params follow T's rules. An empty locale uses the
// invocation's locale. Missing keys and host errors return key.
func TLocale(key, locale string, params ...any) string {
	return translate(abi.TranslationInput{Key: key, Locale: locale, Params: translationParams(params)})
}

// TPlural translates a module-relative key with _one for count 1 and _other
// otherwise. At count 0, a present _zero takes precedence over _other in each
// fallback locale. Params supply named substitutions, including count when needed.
// Missing forms and host errors return the unsuffixed key.
func TPlural(key string, count int, params map[string]any) string {
	return translate(abi.TranslationInput{
		Key:    key,
		Count:  new(int64(count)),
		Params: params,
	})
}

func translationParams(params []any) any {
	if len(params) == 1 {
		if named, ok := params[0].(map[string]any); ok {
			return named
		}
	}

	return params
}

func translate(input abi.TranslationInput) string {
	var value string
	if err := hostcall.Do(hostI18nT, input, &value); err != nil {
		return input.Key
	}

	return value
}
