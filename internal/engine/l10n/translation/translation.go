// Package translation resolves module-relative keys and interpolates backend translations.
package translation

import (
	"fmt"
	"strconv"
	"strings"

	abi "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/djangbahevans/goerp/internal/engine/l10n"
)

type Lookup func(locale, key string) (string, bool)

func Translate(lookup Lookup, moduleName string, input abi.TranslationInput) string {
	if lookup == nil || moduleName == "" {
		return input.Key
	}

	language, _, _ := strings.Cut(input.Locale, "-")
	for _, locale := range []string{input.Locale, language, l10n.PlatformDefaultLocale} {
		key := moduleName + "." + input.Key
		if input.Count != nil {
			switch *input.Count {
			case 0:
				if value, ok := lookup(locale, key+"_zero"); ok {
					return interpolate(value, input.Params)
				}
				key += "_other"
			case 1:
				key += "_one"
			default:
				key += "_other"
			}
		}
		if value, ok := lookup(locale, key); ok {
			return interpolate(value, input.Params)
		}
	}

	return input.Key
}

func interpolate(text string, params any) string {
	var out strings.Builder
	for {
		before, rest, found := strings.Cut(text, "{")
		out.WriteString(before)
		if !found {
			break
		}
		name, after, closed := strings.Cut(rest, "}")
		if !closed {
			out.WriteString("{" + rest)
			break
		}

		value, ok := parameter(params, name)
		if ok {
			fmt.Fprint(&out, value)
		} else {
			out.WriteString("{" + name + "}")
		}
		text = after
	}
	return out.String()
}

func parameter(params any, name string) (any, bool) {
	switch values := params.(type) {
	case map[string]any:
		value, ok := values[name]
		return value, ok
	case []any:
		index, err := strconv.Atoi(name)
		if err == nil && index >= 0 && index < len(values) && strconv.Itoa(index) == name {
			return values[index], true
		}
	}
	return nil, false
}
