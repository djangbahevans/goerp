package tenantimport

import (
	"github.com/djangbahevans/goerp/sdk/go/model"
	"github.com/jackc/pgx/v5"
)

// exportableColumns mirrors internal/engine/tenant/export's own function of
// the same name (unexported there too, duplicated here rather than shared
// across a package boundary for one small helper — same call this codebase
// already made for quoteIdent below). It must select
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

func quoteIdent(name string) string {
	return pgx.Identifier{name}.Sanitize()
}
