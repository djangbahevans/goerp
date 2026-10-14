package schema

import (
	"crypto/sha256"
	"fmt"
	"strings"

	"ariga.io/atlas/sql/schema"
	"github.com/djangbahevans/goerp/internal/engine/modelmeta"
	"github.com/djangbahevans/goerp/sdk/go/model"
)

func addMany2ManyTable(s *schema.Schema, md model.ModelDeclaration, f model.NamedField, moduleName string, tables map[string]*schema.Table, cfg atlasConfig) error {
	d := f.Def
	if err := modelmeta.ValidateMany2Many(md, f); err != nil {
		return err
	}

	if _, exists := s.Table(d.JoinTable); exists {
		return fmt.Errorf("join table %q is already declared", d.JoinTable)
	}
	for _, models := range cfg.models {
		for _, dependency := range models {
			if TableNameFor(dependency) == d.JoinTable {
				return fmt.Errorf("join table %q belongs to a dependency", d.JoinTable)
			}
			for _, field := range dependency.Fields {
				if field.Def.Kind == model.KindMany2Many && field.Def.JoinTable == d.JoinTable {
					return fmt.Errorf("join table %q belongs to a dependency", d.JoinTable)
				}
			}
		}
	}

	owner := tables[md.QualifiedName(moduleName)]
	if owner == nil || md.Backend != "" {
		return fmt.Errorf("Many2Many requires a Postgres-backed declaring model")
	}

	target := tables[d.RelatedModel]
	if target == nil {
		targetModule, _, qualified := strings.Cut(d.RelatedModel, ".")
		if !qualified || !cfg.hard[targetModule] {
			return fmt.Errorf("related_model %q must belong to this module or a hard dependency", d.RelatedModel)
		}
		targetDecl, exists := cfg.models[targetModule][d.RelatedModel]
		if !exists || targetDecl.Backend != "" {
			return fmt.Errorf("related_model %q must be a loaded Postgres-backed model", d.RelatedModel)
		}

		target = schema.NewTable(TableNameFor(targetDecl))
		var keys []*schema.Column
		for _, tf := range targetDecl.Fields {
			if tf.Def.IsPrimaryKey {
				if tf.Def.Kind != model.KindUUID {
					return fmt.Errorf("related_model %q requires a UUID primary key", d.RelatedModel)
				}
				keys = append(keys, schema.NewColumn(tf.Name).SetType(&schema.UUIDType{T: "uuid"}))
			}
		}
		target.AddColumns(keys...).SetPrimaryKey(schema.NewPrimaryKey(keys...))
		// A dependency owns its table; this stand-in only anchors the foreign key.
		target.SetSchema(s)
	}

	ownerPK, err := many2ManyPrimaryKey(owner)
	if err != nil {
		return err
	}
	targetPK, err := many2ManyPrimaryKey(target)
	if err != nil {
		return err
	}

	c1 := schema.NewColumn(d.Column1).SetType(ownerPK.Type.Type)
	c2 := schema.NewColumn(d.Column2).SetType(targetPK.Type.Type)
	join := schema.NewTable(d.JoinTable).AddColumns(c1, c2).SetPrimaryKey(schema.NewPrimaryKey(c1, c2))
	join.AddForeignKeys(
		schema.NewForeignKey(joinForeignKeyName(d.JoinTable, d.Column1)).SetTable(join).AddColumns(c1).SetRefTable(owner).AddRefColumns(ownerPK).SetOnDelete(schema.Cascade),
		schema.NewForeignKey(joinForeignKeyName(d.JoinTable, d.Column2)).SetTable(join).AddColumns(c2).SetRefTable(target).AddRefColumns(targetPK).SetOnDelete(schema.Cascade),
	)
	s.AddTables(join)
	return nil
}

func joinForeignKeyName(table, column string) string {
	name := table + "_" + column + "_fkey"
	if len(name) <= 63 {
		return name
	}
	digest := sha256.Sum256([]byte(name))
	return fmt.Sprintf("%s_%x", name[:46], digest[:8])
}

func many2ManyPrimaryKey(table *schema.Table) (*schema.Column, error) {
	pk, ok := primaryKeyColumnOf(table)
	if !ok || len(table.PrimaryKey.Parts) != 1 {
		return nil, fmt.Errorf("Many2Many table %q requires a single UUID primary key", table.Name)
	}
	if _, ok := pk.Type.Type.(*schema.UUIDType); !ok {
		return nil, fmt.Errorf("Many2Many table %q requires a UUID primary key", table.Name)
	}
	return pk, nil
}
