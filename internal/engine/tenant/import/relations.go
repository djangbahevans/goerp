package tenantimport

import (
	"context"
	"database/sql"
	"fmt"
	"maps"
	"slices"
	"uuid"

	"github.com/djangbahevans/goerp/internal/engine/registry"
	"github.com/djangbahevans/goerp/internal/engine/tenant/transfer"
)

func importOrder(man manifest, snap *registry.RegistrySnapshot) ([]manifestModule, []string, error) {
	selected := make(map[string]manifestModule, len(man.Modules))
	for _, mod := range man.Modules {
		selected[mod.Name] = mod
	}

	var modules []manifestModule
	var schemas []string
	visited := make(map[string]bool)
	visiting := make(map[string]bool)
	var visit func(string) error
	visit = func(name string) error {
		if visited[name] {
			return nil
		}
		if visiting[name] {
			return fmt.Errorf("module dependency cycle at %q", name)
		}

		mod, ok := snap.Modules()[name]
		if !ok {
			return fmt.Errorf("module dependency %q is not installed", name)
		}
		visiting[name] = true
		for _, dependency := range mod.Manifest.DependsOn {
			if err := visit(dependency); err != nil {
				return err
			}
		}
		delete(visiting, name)
		visited[name] = true
		schemas = append(schemas, name)
		if archived, ok := selected[name]; ok {
			modules = append(modules, archived)
		}
		return nil
	}
	for _, name := range slices.Sorted(maps.Keys(selected)) {
		if err := visit(name); err != nil {
			return nil, nil, err
		}
	}
	return modules, schemas, nil
}

func loadLinks(ctx context.Context, tx *sql.Tx, module string, relations []transfer.Relation, links []transfer.Link) error {
	byField := make(map[string]transfer.Relation)
	for _, relation := range relations {
		if relation.Module == module {
			byField[relation.Model+":"+relation.Field] = relation
		}
	}

	for _, link := range links {
		relation, ok := byField[link.Model+":"+link.Field]
		if !ok {
			return fmt.Errorf("archive references unknown or restricted relation %s.%s in module %q", link.Model, link.Field, module)
		}
		if _, err := uuid.Parse(link.SourceID); err != nil {
			return fmt.Errorf("relation %s.%s source ID: %w", link.Model, link.Field, err)
		}
		if _, err := uuid.Parse(link.TargetID); err != nil {
			return fmt.Errorf("relation %s.%s target ID: %w", link.Model, link.Field, err)
		}

		query := fmt.Sprintf("INSERT INTO %s (%s, %s) VALUES ($1, $2) ON CONFLICT DO NOTHING",
			quoteIdent(relation.Table), quoteIdent(relation.Column1), quoteIdent(relation.Column2))
		if _, err := tx.ExecContext(ctx, query, link.SourceID, link.TargetID); err != nil {
			return fmt.Errorf("insert relation %s.%s: %w", link.Model, link.Field, err)
		}
	}
	return nil
}
