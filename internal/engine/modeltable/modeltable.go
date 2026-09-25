// Package modeltable resolves a model declaration's Postgres table name.
// A leaf package so that both schema sync and the module loader can
// depend on it without importing each other.
package modeltable

import (
	"strings"
	"unicode"

	"github.com/djangbahevans/goerp/sdk/go/model"
)

// Name resolves md's Postgres table name: its explicit Table override, or
// snakeCase(Name) otherwise.
func Name(md model.ModelDeclaration) string {
	if md.Table != "" {
		return md.Table
	}
	return snakeCase(md.Name)
}

// snakeCase converts a model name to its default table name: "." becomes
// "_", and each upper-case letter following a lower-case one starts a new
// "_"-separated word.
func snakeCase(name string) string {
	var b strings.Builder
	prevLower := false
	for _, r := range name {
		switch {
		case r == '.':
			b.WriteByte('_')
			prevLower = false
		case unicode.IsUpper(r):
			if prevLower {
				b.WriteByte('_')
			}
			b.WriteRune(unicode.ToLower(r))
			prevLower = false
		default:
			b.WriteRune(r)
			prevLower = true
		}
	}
	return b.String()
}
