package schema

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"strings"

	"ariga.io/atlas/sql/migrate"
	"ariga.io/atlas/sql/schema"
	"github.com/djangbahevans/goerp/sdk/go/model"
)

type extensionObject struct {
	table string
	kind  string
	name  string
	owner string
}

func extensionObjects(ctx context.Context, sess *SchemaSyncSession) ([]extensionObject, error) {
	rows, err := sess.execQuerier().QueryContext(ctx, `
		SELECT table_name, object_kind, object_name, extension_module
		FROM system.model_extension_objects WHERE tenant_id = $1`, sess.tenantID)
	if err != nil {
		return nil, fmt.Errorf("read extension ownership: %w", err)
	}
	defer rows.Close()

	var objects []extensionObject
	for rows.Next() {
		var object extensionObject
		if err := rows.Scan(&object.table, &object.kind, &object.name, &object.owner); err != nil {
			return nil, err
		}
		objects = append(objects, object)
	}
	return objects, rows.Err()
}

func preserveExtensionObjects(ctx context.Context, sess *SchemaSyncSession, live, desired *schema.Schema) error {
	objects, err := extensionObjects(ctx, sess)
	if err != nil {
		return err
	}
	for _, object := range objects {
		if object.kind == "type" && object.owner != sess.moduleName {
			match := func(o schema.Object) bool {
				enum, ok := o.(*schema.EnumType)
				return ok && enum.T == object.name
			}
			if _, collision := desired.Object(match); collision {
				return fmt.Errorf("enum type %q belongs to extension module %q", object.name, object.owner)
			}
			if enum, exists := live.Object(match); exists {
				desired.AddObjects(enum)
			}
			continue
		}
		from, exists := live.Table(object.table)
		to, managed := desired.Table(object.table)
		if !exists || !managed || object.owner == sess.moduleName {
			continue
		}

		switch object.kind {
		case "column":
			if _, collision := to.Column(object.name); collision {
				return fmt.Errorf("table %q column %q belongs to extension module %q", object.table, object.name, object.owner)
			}
			if column, exists := from.Column(object.name); exists {
				to.AddColumns(column)
			}
		case "index":
			if _, collision := to.Index(object.name); collision {
				return fmt.Errorf("table %q index %q belongs to extension module %q", object.table, object.name, object.owner)
			}
			if index, exists := from.Index(object.name); exists {
				to.AddIndexes(index)
			}
		case "check":
			for _, check := range from.Checks() {
				if check.Name == object.name {
					to.AddChecks(check)
				}
			}
		case "foreign_key":
			if fk, exists := from.ForeignKey(object.name); exists {
				to.AddForeignKeys(fk)
			}
		}
	}
	return nil
}

func (e *SchemaDiffEngine) diffExtensions(ctx context.Context, sess *SchemaSyncSession, driver migrate.Driver, extensions []model.ModelExtension, types []model.TypeDeclaration) ([]schema.Change, error) {
	if len(extensions) == 0 {
		return nil, nil
	}
	if sess.manifest.Type != "field_extension" || e.cfg.ModelSource == nil {
		return nil, fmt.Errorf("model extensions require a field_extension module and a loaded model source")
	}

	objects, err := extensionObjects(ctx, sess)
	if err != nil {
		return nil, err
	}
	owners := make(map[string]string, len(objects))
	for _, object := range objects {
		owners[object.table+"/"+object.kind+"/"+object.name] = object.owner
	}

	var targets []model.ModelExtension
	for _, ext := range extensions {
		index := slices.IndexFunc(targets, func(target model.ModelExtension) bool { return target.Model == ext.Model })
		if index < 0 {
			targets = append(targets, model.ModelExtension{Model: ext.Model})
			index = len(targets) - 1
		}

		targets[index].Fields = append(targets[index].Fields, ext.Fields...)
		targets[index].Indexes = append(targets[index].Indexes, ext.Indexes...)
	}

	var changes []schema.Change
	for _, ext := range targets {
		owner, _, _ := strings.Cut(ext.Model, ".")
		if sess.manifest.Schema.ExtendsModule == nil || *sess.manifest.Schema.ExtendsModule != owner || !slices.Contains(sess.ExtendsModels(), ext.Model) {
			return nil, fmt.Errorf("model extension target %q is not authorized by schema.extends_models", ext.Model)
		}
		models, loaded := e.cfg.ModelSource.ModelDeclarations(owner)
		index := slices.IndexFunc(models, func(md model.ModelDeclaration) bool { return md.QualifiedName(owner) == ext.Model })
		if !loaded || index < 0 || models[index].Backend != "" {
			return nil, fmt.Errorf("model extension target %q is not a loaded table-backed model", ext.Model)
		}

		decls := make(map[string]model.ModelDeclaration, len(models))
		for _, md := range models {
			md.Fields = slices.Clone(md.Fields)
			for _, target := range targets {
				if target.Model != md.QualifiedName(owner) {
					continue
				}

				for _, field := range target.Fields {
					md.Fields = slices.DeleteFunc(md.Fields, func(f model.NamedField) bool { return f.Name == field.Name })
					md.Fields = append(md.Fields, field)
				}
			}

			decls[md.QualifiedName(owner)] = md
		}

		for _, field := range ext.Fields {
			if field.Def.Kind == model.KindOne2Many {
				if err := validateOne2Many(decls[ext.Model], field, owner, decls); err != nil {
					return nil, fmt.Errorf("model extension %q field %q: %w", ext.Model, field.Name, err)
				}
			}

			if field.Def.IsTree && field.Def.RelatedModel != ext.Model {
				return nil, fmt.Errorf("model extension %q field %q: .Tree() requires a self-referential relation", ext.Model, field.Name)
			}
		}

		table := TableNameFor(models[index])
		inspected, err := driver.InspectSchema(ctx, "tenant_"+sess.tenantSlug, &schema.InspectOptions{Tables: []string{table}})
		if err != nil {
			return nil, fmt.Errorf("inspect model extension target %q: %w", ext.Model, err)
		}
		live, exists := inspected.Table(table)
		if !exists {
			return nil, fmt.Errorf("model extension target %q table %q does not exist", ext.Model, table)
		}
		desired := *live
		desired.Columns = slices.Clone(live.Columns)
		desired.Indexes = slices.Clone(live.Indexes)
		desired.ForeignKeys = slices.Clone(live.ForeignKeys)
		desired.Attrs = slices.Clone(live.Attrs)

		enums := make(map[string]*schema.EnumType, len(types))
		for _, td := range types {
			enums[td.Name] = &schema.EnumType{T: td.Name, Schema: live.Schema, Values: td.Values}
		}
		added, err := toAtlasTable(model.ModelDeclaration{Name: ext.Model, Table: table, Fields: ext.Fields}, enums)
		if err != nil {
			return nil, fmt.Errorf("model extension %q: %w", ext.Model, err)
		}

		for _, column := range added.Columns {
			if _, exists := live.Column(column.Name); exists && owners[table+"/column/"+column.Name] != sess.moduleName {
				return nil, fmt.Errorf("model extension %q cannot redefine existing column %q", ext.Model, column.Name)
			}
			if !column.Type.Null && column.Default == nil {
				return nil, fmt.Errorf("model extension %q column %q must be nullable or have a default", ext.Model, column.Name)
			}
			desired.Columns = slices.DeleteFunc(desired.Columns, func(c *schema.Column) bool { return c.Name == column.Name })
			desired.AddColumns(column)
		}
		for _, check := range added.Checks() {
			desired.Attrs = slices.DeleteFunc(desired.Attrs, func(attr schema.Attr) bool {
				c, ok := attr.(*schema.Check)
				return ok && c.Name == check.Name
			})
			desired.AddChecks(check)
		}

		cfg := atlasConfig{hard: map[string]bool{}, soft: map[string]bool{}, models: map[string]map[string]model.ModelDeclaration{}}
		for _, option := range e.dependencyOptions(sess.manifest) {
			option(&cfg)
		}
		for _, field := range ext.Fields {
			if field.Def.Kind == model.KindMany2One {
				fkName := table + "_" + field.Name + "_fkey"
				desired.ForeignKeys = slices.DeleteFunc(desired.ForeignKeys, func(f *schema.ForeignKey) bool { return f.Symbol == fkName })
				if err := addCrossModuleForeignKey(live.Schema, &desired, field, cfg); err != nil {
					return nil, fmt.Errorf("model extension %q field %q: %w", ext.Model, field.Name, err)
				}
			}
		}
		indexes := slices.Clone(added.Indexes)
		for _, declaration := range ext.Indexes {
			index, err := toAtlasIndex(&desired, declaration)
			if err != nil {
				return nil, fmt.Errorf("model extension %q index %q: %w", ext.Model, declaration.Name, err)
			}
			indexes = append(indexes, index)
		}
		for _, index := range indexes {
			if _, exists := live.Index(index.Name); exists && owners[table+"/index/"+index.Name] != sess.moduleName {
				return nil, fmt.Errorf("model extension %q cannot redefine existing index %q", ext.Model, index.Name)
			}
			desired.Indexes = slices.DeleteFunc(desired.Indexes, func(i *schema.Index) bool { return i.Name == index.Name })
			desired.AddIndexes(index)
		}

		diff, err := driver.TableDiff(live, &desired)
		if err != nil {
			return nil, err
		}
		for _, change := range diff {
			switch change.(type) {
			case *schema.AddColumn, *schema.AddIndex, *schema.AddCheck, *schema.AddForeignKey:
			default:
				return nil, fmt.Errorf("model extension %q only permits additions, got %T", ext.Model, change)
			}
		}
		if len(diff) > 0 {
			changes = append(changes, &schema.ModifyTable{T: &desired, Changes: diff})
		}
	}
	return changes, nil
}

func reserveExtensionObjects(ctx context.Context, sess *SchemaSyncSession, changes []schema.Change) error {
	if sess.manifest.Type != "field_extension" {
		return nil
	}
	for _, change := range explodeChanges(changes) {
		var kind, name, table string
		if change.table != nil {
			table = change.table.Name
		}
		switch c := change.change.(type) {
		case *schema.AddObject:
			enum, ok := c.O.(*schema.EnumType)
			if !ok {
				continue
			}
			kind, name = "type", enum.T
		case *schema.AddColumn:
			kind, name = "column", c.C.Name
		case *schema.AddIndex:
			kind, name = "index", c.I.Name
		case *schema.AddCheck:
			kind, name = "check", c.C.Name
		case *schema.AddForeignKey:
			kind, name = "foreign_key", c.F.Symbol
		default:
			return fmt.Errorf("model extensions only permit additions, got %T", change.change)
		}
		var owner string
		err := sess.conn.QueryRowContext(ctx, `
			INSERT INTO system.model_extension_objects (tenant_id, table_name, object_kind, object_name, extension_module)
			VALUES ($1, $2, $3, $4, $5)
			ON CONFLICT (tenant_id, table_name, object_kind, object_name) DO UPDATE
			SET extension_module = EXCLUDED.extension_module
			WHERE model_extension_objects.extension_module = EXCLUDED.extension_module
			RETURNING extension_module`, sess.tenantID, table, kind, name, sess.moduleName).Scan(&owner)
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("extension object %s %q on %q belongs to another module", kind, name, table)
		}
		if err != nil {
			return fmt.Errorf("reserve extension object: %w", err)
		}
	}
	return nil
}
