package tenantimport

import (
	"github.com/djangbahevans/goerp/internal/engine/modelmeta"
	"github.com/djangbahevans/goerp/sdk/go/model"
	"github.com/jackc/pgx/v5"
)

func exportableColumns(md model.ModelDeclaration) []string {
	cols := make([]string, 0, len(md.Fields))
	for _, f := range md.Fields {
		def := f.Def
		if def.Kind == model.KindOne2Many || modelmeta.IsJoinRelation(def.Kind) {
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
