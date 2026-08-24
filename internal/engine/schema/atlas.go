package schema

import (
	"fmt"
	"strings"
	"unicode"

	"ariga.io/atlas/sql/postgres"
	"ariga.io/atlas/sql/schema"
	"github.com/djangbahevans/goerp/sdk/go/model"
)

func ToAtlasSchema(schemaName string, modelDecls []model.ModelDeclaration, typeDecls []model.TypeDeclaration) (*schema.Schema, error) {
	s := schema.New(schemaName)

	enumTypes := make(map[string]*schema.EnumType, len(typeDecls))
	for _, td := range typeDecls {
		e := &schema.EnumType{T: td.Name, Schema: s, Values: td.Values}
		s.AddObjects(e)
		enumTypes[td.Name] = e
	}

	for _, md := range modelDecls {
		t, err := toAtlasTable(md, enumTypes)
		if err != nil {
			return nil, fmt.Errorf("model %s: %w", md.Name, err)
		}
		s.AddTables(t)
	}

	return s, nil
}

// TableNameFor resolves a model declaration's Postgres table name: its
// explicit Table override, or snake_case(Name) otherwise.
func TableNameFor(md model.ModelDeclaration) string {
	if md.Table != "" {
		return md.Table
	}
	return snakeCase(md.Name)
}

func toAtlasTable(md model.ModelDeclaration, enumTypes map[string]*schema.EnumType) (*schema.Table, error) {
	tableName := TableNameFor(md)

	t := schema.NewTable(tableName)

	var pk []*schema.Column
	for _, f := range md.Fields {
		col, err := toAtlasColumn(f.Name, f.Def, enumTypes)
		if err != nil {
			return nil, fmt.Errorf("field %s: %w", f.Name, err)
		}
		t.AddColumns(col)
		if f.Def.IsPrimaryKey {
			pk = append(pk, col)
		}
		if f.Def.Kind == model.KindSelection && len(f.Def.SelectionValues) > 0 {
			t.AddChecks(selectionCheck(tableName, f.Name, f.Def.SelectionValues))
		}
	}
	if len(pk) > 0 {
		t.SetPrimaryKey(schema.NewPrimaryKey(pk...))
	}

	for _, idx := range md.Indexes {
		i, err := toAtlasIndex(t, idx)
		if err != nil {
			return nil, fmt.Errorf("index %s: %w", idx.Name, err)
		}
		t.AddIndexes(i)
	}

	return t, nil
}

func toAtlasColumn(name string, f model.FieldDef, enumTypes map[string]*schema.EnumType) (*schema.Column, error) {
	c := schema.NewColumn(name).SetNull(!f.IsRequired && !f.IsPrimaryKey)

	switch f.Kind {
	case model.KindChar:
		if f.Length > 0 {
			c.SetType(&schema.StringType{T: postgres.TypeVarChar, Size: f.Length})
		} else {
			c.SetType(&schema.StringType{T: postgres.TypeText})
		}
	case model.KindText:
		c.SetType(&schema.StringType{T: postgres.TypeText})
	case model.KindInteger:
		c.SetType(&schema.IntegerType{T: postgres.TypeInteger})
	case model.KindBigInt:
		c.SetType(&schema.IntegerType{T: postgres.TypeBigInt})
	case model.KindFloat:
		c.SetType(&schema.FloatType{T: postgres.TypeDouble})
	case model.KindDecimal:
		c.SetType(&schema.DecimalType{T: postgres.TypeNumeric, Precision: f.Precision, Scale: f.Scale})
	case model.KindBoolean:
		c.SetType(&schema.BoolType{T: postgres.TypeBoolean})
	case model.KindUUID:
		c.SetType(&schema.UUIDType{T: postgres.TypeUUID})
	case model.KindTimestampTZ:
		c.SetType(&schema.TimeType{T: postgres.TypeTimestampTZ})
	case model.KindDate:
		c.SetType(&schema.TimeType{T: postgres.TypeDate})
	case model.KindTime:
		c.SetType(&schema.TimeType{T: postgres.TypeTime})
	case model.KindJSONB:
		c.SetType(&schema.JSONType{T: postgres.TypeJSONB})
	case model.KindBytea:
		c.SetType(&schema.BinaryType{T: postgres.TypeBytea})
	case model.KindSelection:
		// Stored as TEXT; the CHECK constraint enforcing SelectionValues is
		// added on the table by the caller (toAtlasTable), not here — Atlas
		// checks are table-level, not column-level.
		c.SetType(&schema.StringType{T: postgres.TypeText})
	case model.KindEnum:
		e, ok := enumTypes[f.EnumType]
		if !ok {
			return nil, fmt.Errorf("field %q references undeclared enum type %q (not present in Schema.Types)", name, f.EnumType)
		}
		c.SetType(e)
	default:
		return nil, fmt.Errorf("unknown field kind %d", f.Kind)
	}

	if f.DefaultExpr != nil {
		c.SetDefault(&schema.RawExpr{X: *f.DefaultExpr})
	}

	return c, nil
}

func selectionCheck(table, field string, values []string) *schema.Check {
	quoted := make([]string, len(values))
	for i, v := range values {
		quoted[i] = "'" + strings.ReplaceAll(v, "'", "''") + "'"
	}
	return &schema.Check{
		Name: fmt.Sprintf("%s_%s_check", table, field),
		Expr: fmt.Sprintf("%s IN (%s)", quoteIdent(field), strings.Join(quoted, ", ")),
	}
}

func toAtlasIndex(t *schema.Table, ni model.NamedIndex) (*schema.Index, error) {
	idx := schema.NewIndex(ni.Name).SetUnique(ni.Def.IsUnique)

	switch ni.Def.Kind {
	case model.KindBTree:
		idx.AddAttrs(&postgres.IndexType{T: "btree"})
	case model.KindGIN:
		idx.AddAttrs(&postgres.IndexType{T: "gin"})
	default:
		return nil, fmt.Errorf("unknown index kind %d", ni.Def.Kind)
	}

	for i, col := range ni.Def.Columns {
		part := schema.NewIndexPart()
		part.SeqNo = i
		if c, ok := t.Column(col); ok {
			part.C = c
		} else {
			part.X = &schema.RawExpr{X: col}
		}
		if ni.Def.Ops != "" {
			part.AddAttrs(&postgres.IndexOpClass{Name: ni.Def.Ops})
		}
		idx.AddParts(part)
	}

	if ni.Def.WhereExpr != "" {
		idx.AddAttrs(&postgres.IndexPredicate{P: ni.Def.WhereExpr})
	}

	return idx, nil
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
