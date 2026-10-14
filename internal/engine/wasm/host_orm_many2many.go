package wasm

import (
	"context"
	"database/sql"
	"fmt"
	"slices"
	"strings"
	"uuid"

	abiv1 "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/djangbahevans/goerp/internal/engine/modelmeta"
	"github.com/djangbahevans/goerp/internal/engine/modeltable"
	"github.com/djangbahevans/goerp/sdk/go/model"
)

func relationIDs(value any) ([]string, error) {
	var ids []string
	switch v := value.(type) {
	case []string:
		ids = slices.Clone(v)
	case []any:
		ids = make([]string, len(v))
		for i, item := range v {
			id, ok := item.(string)
			if !ok {
				return nil, fmt.Errorf("expected an array of UUID strings")
			}
			ids[i] = id
		}
	default:
		return nil, fmt.Errorf("expected an array of UUID strings")
	}

	for i, id := range ids {
		parsed, err := uuid.Parse(id)
		if err != nil {
			return nil, fmt.Errorf("invalid related record UUID %q", id)
		}
		ids[i] = parsed.String()
	}

	slices.Sort(ids)
	return slices.Compact(ids), nil
}

func relationValueError(field string, err error) *abiv1.HostError {
	return &abiv1.HostError{Code: abiv1.ErrCodeValidationFailed, Message: "field " + field + ": " + err.Error(), Details: map[string]any{"field": field}}
}

func prepareMany2ManyWrites(ctx context.Context, tx *sql.Tx, mc *ModuleContext, md model.ModelDeclaration, qualifiedModel string, record map[string]any) (map[string][]string, *abiv1.HostError) {
	sets := map[string][]string{}
	for _, f := range md.Fields {
		value, present := record[f.Name]
		if f.Def.Kind != model.KindMany2Many || !present {
			continue
		}
		if _, denied := writeDeniedBy(mc, qualifiedModel, f.Name); denied {
			continue // buildAssignment handles rejection; ignored fields retain their links.
		}

		ids, err := relationIDs(value)
		if err != nil {
			return nil, relationValueError(f.Name, err)
		}
		target, _, ok := resolveRelationTarget(mc, f.Def.RelatedModel)
		if !ok || target.Backend != "" {
			return nil, relationValueError(f.Name, fmt.Errorf("related model is unavailable"))
		}

		if len(ids) > 0 {
			pk, _ := primaryKeyColumn(target)
			// Row locks would require target UPDATE access. The foreign key protects
			// against deletion between this visibility check and inserting the links.
			query := fmt.Sprintf("SELECT %s FROM %s WHERE %s", quoteIdentORM(pk), quoteIdentORM(modeltable.Name(target)), activeModelWhere(target, quoteIdentORM(pk)+" = ANY($1::uuid[])"))
			rows, err := tx.QueryContext(ctx, query, "{"+strings.Join(ids, ",")+"}")
			if err != nil {
				return nil, ormSQLError(err)
			}
			found, err := scanRowsToMaps(rows)
			if err != nil {
				return nil, ormSQLError(err)
			}
			if len(found) != len(ids) {
				return nil, relationValueError(f.Name, fmt.Errorf("related record is unavailable"))
			}
		}

		sets[f.Name] = ids
	}
	return sets, nil
}

// replaceMany2Many stores each set and returns the fields whose links changed.
func replaceMany2Many(ctx context.Context, tx *sql.Tx, md model.ModelDeclaration, ownerID any, sets map[string][]string, row map[string]any) ([]string, *abiv1.HostError) {
	var changed []string
	for _, f := range md.Fields {
		ids, present := sets[f.Name]
		if !present {
			continue
		}

		d := f.Def
		query := fmt.Sprintf("DELETE FROM %s WHERE %s = $1 RETURNING %s::text", quoteIdentORM(d.JoinTable), quoteIdentORM(d.Column1), quoteIdentORM(d.Column2))
		rows, err := tx.QueryContext(ctx, query, ownerID)
		if err != nil {
			return nil, ormSQLError(err)
		}
		removed, err := scanRowsToMaps(rows)
		if err != nil {
			return nil, ormSQLError(err)
		}

		if len(ids) > 0 {
			query := fmt.Sprintf("INSERT INTO %s (%s, %s) SELECT $1, unnest($2::uuid[])", quoteIdentORM(d.JoinTable), quoteIdentORM(d.Column1), quoteIdentORM(d.Column2))
			if _, err := tx.ExecContext(ctx, query, ownerID, "{"+strings.Join(ids, ",")+"}"); err != nil {
				return nil, ormSQLError(err)
			}
		}

		previous := make([]string, len(removed))
		for i, link := range removed {
			previous[i] = fmt.Sprint(link[d.Column2])
		}
		slices.Sort(previous)
		if !slices.Equal(previous, ids) {
			changed = append(changed, f.Name)
		}
		row[f.Name] = ids
	}
	return changed, nil
}

func relationSelectColumn(md model.ModelDeclaration, name string) string {
	if f, ok := fieldByName(md, name); ok && f.Def.Kind == model.KindMany2Many {
		return "NULL AS " + quoteIdentORM(name)
	}
	return quoteIdentORM(name)
}

func loadMany2ManyIDs(ctx context.Context, tx *sql.Tx, mc *ModuleContext, md model.ModelDeclaration, columns []string, records []map[string]any) error {
	pk, _ := primaryKeyColumn(md)
	for _, f := range md.Fields {
		if f.Def.Kind != model.KindMany2Many || !slices.Contains(columns, f.Name) || len(records) == 0 {
			continue
		}

		target, _, ok := resolveRelationTarget(mc, f.Def.RelatedModel)
		if !ok {
			return fmt.Errorf("related model %q is unavailable", f.Def.RelatedModel)
		}
		targetPK, _ := primaryKeyColumn(target)

		args := make([]any, len(records))
		placeholders := make([]string, len(records))
		byID := map[string]map[string]any{}
		for i, row := range records {
			args[i] = row[pk]
			placeholders[i] = fmt.Sprintf("$%d", i+1)
			byID[fmt.Sprint(row[pk])] = row
			row[f.Name] = []string{}
		}

		d := f.Def
		// Filtering targets in a subquery applies their RLS and soft deletion before
		// even the bare IDs become visible to the parent response.
		query := fmt.Sprintf("SELECT j.%s, j.%s FROM %s j WHERE j.%s IN (%s) AND j.%s IN (SELECT %s FROM %s WHERE %s) ORDER BY j.%s",
			quoteIdentORM(d.Column1), quoteIdentORM(d.Column2), quoteIdentORM(d.JoinTable), quoteIdentORM(d.Column1), strings.Join(placeholders, ", "), quoteIdentORM(d.Column2), quoteIdentORM(targetPK), quoteIdentORM(modeltable.Name(target)), activeModelWhere(target, "true"), quoteIdentORM(d.Column2))
		rows, err := tx.QueryContext(ctx, query, args...)
		if err != nil {
			return err
		}
		links, err := scanRowsToMaps(rows)
		if err != nil {
			return err
		}

		for _, link := range links {
			row := byID[fmt.Sprint(link[d.Column1])]
			row[f.Name] = append(row[f.Name].([]string), fmt.Sprint(link[d.Column2]))
		}
	}
	return nil
}

func expandMany2Many(ctx context.Context, tx *sql.Tx, mc *ModuleContext, f model.NamedField, records []map[string]any, maskRelated bool) error {
	readKey := modelmeta.ReadKey(f.Name, f.Def.Kind.String())
	ids := []string{}
	for _, row := range records {
		if value, present := row[f.Name]; present {
			if set, ok := value.([]string); ok {
				ids = append(ids, set...)
				row[readKey] = []map[string]any{}
			} else {
				row[readKey] = value
			}
		}
	}
	if len(ids) == 0 {
		return nil
	}
	slices.Sort(ids)
	ids = slices.Compact(ids)

	target, _, _ := resolveRelationTarget(mc, f.Def.RelatedModel)
	pk, _ := primaryKeyColumn(target)
	cols := []string{}
	for _, tf := range target.Fields {
		if tf.Def.Kind != model.KindMany2Many && tf.Def.Kind != model.KindOne2Many && (!tf.Def.IsComputed || tf.Def.IsStored) {
			cols = append(cols, tf.Name)
		}
	}

	args := make([]any, len(ids))
	placeholders := make([]string, len(ids))
	for i, id := range ids {
		args[i] = id
		placeholders[i] = fmt.Sprintf("$%d", i+1)
	}
	query := fmt.Sprintf("SELECT %s FROM %s WHERE %s", joinQuoted(cols), quoteIdentORM(modeltable.Name(target)), activeModelWhere(target, fmt.Sprintf("%s IN (%s)", quoteIdentORM(pk), strings.Join(placeholders, ", "))))
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return err
	}
	related, err := scanRowsToMaps(rows)
	if err != nil {
		return err
	}

	byID := map[string]map[string]any{}
	for _, row := range related {
		byID[fmt.Sprint(row[pk])] = row
	}
	if maskRelated {
		applyFieldMasking(mc, f.Def.RelatedModel, related)
	}

	for _, row := range records {
		if set, ok := row[f.Name].([]string); ok {
			for _, id := range set {
				if related, exists := byID[id]; exists {
					row[readKey] = append(row[readKey].([]map[string]any), related)
				}
			}
		}
	}
	return nil
}
