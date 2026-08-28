package tenantexport

import (
	"strings"
	"unicode"

	"github.com/djangbahevans/goerp/sdk/go/model"
	"github.com/jackc/pgx/v5"
)

// exportableColumns is the set of a model's own field names to dump —
// every stored, non-relation field except one carrying a restrictive
// .Access() rule (cli-reference.md §5: "never exports... any field
// carrying a restrictive .Access() field-security rule"). Unlike
// host.orm.read's applyFieldMasking (internal/engine/wasm/host_orm.go),
// which masks a restricted field per the live caller's own permissions,
// export has no caller to check permissions against — any declared
// ReadPermission at all excludes the column outright.
func exportableColumns(md model.ModelDeclaration) []string {
	cols := make([]string, 0, len(md.Fields))
	for _, f := range md.Fields {
		def := f.Def
		if def.Kind == model.KindOne2Many {
			continue // no backing column — lives on the child's own Many2One
		}
		if def.IsComputed && !def.IsStored {
			continue // computed fresh on read, not persisted
		}
		if def.ReadPermission != "" {
			continue
		}
		cols = append(cols, f.Name)
	}
	return cols
}

// primaryKeyColumn returns md's declared primary key field name, if any.
func primaryKeyColumn(md model.ModelDeclaration) (string, bool) {
	for _, f := range md.Fields {
		if f.Def.IsPrimaryKey {
			return f.Name, true
		}
	}
	return "", false
}

// tableNameFor mirrors internal/engine/wasm/host_orm.go's own
// tableNameForORM/snakeCaseORM (unexported there, duplicated here rather
// than exported across a package boundary for one small helper — same
// call this codebase already made for offboarder.go's jobIDPrefix/
// encodeJobID vs. adminapi's).
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
