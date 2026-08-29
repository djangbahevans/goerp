package tenantimport

import (
	"strings"
	"unicode"

	"github.com/djangbahevans/goerp/sdk/go/model"
	"github.com/jackc/pgx/v5"
)

// exportableColumns mirrors internal/engine/tenant/export's own function of
// the same name (unexported there too, duplicated here rather than shared
// across a package boundary for one small helper — same call this codebase
// already made for tableNameFor/quoteIdent/snakeCase below). It must select
// the exact same column set export dumped, so a module's archive lines
// (built from exportableColumns at export time) map back onto the columns
// this side expects to bind values for.
func exportableColumns(md model.ModelDeclaration) []string {
	cols := make([]string, 0, len(md.Fields))
	for _, f := range md.Fields {
		def := f.Def
		if def.Kind == model.KindOne2Many {
			continue
		}
		if def.IsComputed && !def.IsStored {
			continue
		}
		if def.ReadPermission != "" {
			continue
		}
		cols = append(cols, f.Name)
	}
	return cols
}

// tableNameFor mirrors tenantexport's own tableNameFor/snakeCase.
func tableNameFor(md model.ModelDeclaration) string {
	if md.Table != "" {
		return md.Table
	}
	return snakeCase(md.Name)
}

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

func quoteIdent(name string) string {
	return pgx.Identifier{name}.Sanitize()
}
