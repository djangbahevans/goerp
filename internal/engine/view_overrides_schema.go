package engine

import (
	"cmp"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"slices"

	"github.com/djangbahevans/goerp/internal/engine/manifest"
	"github.com/djangbahevans/goerp/internal/engine/registry"
	"github.com/djangbahevans/goerp/internal/engine/tenantschema"
)

type viewOverrideQuerier interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func loadViewOverrides(ctx context.Context, db viewOverrideQuerier, slug string) ([]viewOverrideResponse, error) {
	query := fmt.Sprintf("SELECT to_jsonb(row) FROM %s.view_overrides row ORDER BY view_name, element, property", tenantschema.Name(slug))
	rows, err := db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("query view overrides: %w", err)
	}
	defer rows.Close()

	var out []viewOverrideResponse
	for rows.Next() {
		var raw []byte
		var row viewOverrideResponse
		if err := rows.Scan(&raw); err != nil {
			return nil, fmt.Errorf("scan view override: %w", err)
		}
		if err := json.Unmarshal(raw, &row); err != nil {
			return nil, fmt.Errorf("decode view override: %w", err)
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read view overrides: %w", err)
	}
	return out, nil
}

type elementOverrides map[string]map[string]any

func overridesForView(rows []viewOverrideResponse, viewName string) elementOverrides {
	out := elementOverrides{}
	for _, row := range rows {
		if row.ViewName != viewName {
			continue
		}
		value, err := overrideValue(row.Property, row.Value)
		if err != nil {
			continue
		}
		if out[row.Element] == nil {
			out[row.Element] = map[string]any{}
		}
		out[row.Element][row.Property] = value
	}
	return out
}

func schemaWithViewOverrides(base *registry.SchemaResponse, rows []viewOverrideResponse) (*registry.SchemaResponse, error) {
	if len(rows) == 0 {
		return base, nil
	}

	out := *base
	out.Modules = maps.Clone(base.Modules)
	changed := false
	for name, mod := range base.Modules {
		copyMod := *mod
		copyMod.Views = slices.Clone(mod.Views)
		for i, view := range mod.Views {
			patches := overridesForView(rows, view.Name)
			if len(patches) == 0 {
				continue
			}
			members, err := viewMembers(view)
			if err != nil {
				return nil, err
			}
			matched := false
			visitViewElements(members, view.Resource, base, func(element map[string]any, _ string) {
				for property, value := range patches[elementName(element)] {
					element[property] = value
					matched = true
				}
			})
			if !matched {
				continue
			}

			orderViewElements(members)
			raw, err := json.Marshal(members, json.Deterministic(true))
			if err != nil {
				return nil, fmt.Errorf("encode patched view: %w", err)
			}
			if err := json.Unmarshal(raw, &copyMod.Views[i]); err != nil {
				return nil, fmt.Errorf("decode patched view: %w", err)
			}
			changed = true
		}
		out.Modules[name] = &copyMod
	}
	if !changed {
		return base, nil
	}

	raw, err := json.Marshal(&out, json.Deterministic(true))
	if err != nil {
		return nil, fmt.Errorf("hash patched schema: %w", err)
	}
	// Custom view marshalers can emit maps without inheriting Deterministic.
	canonical := jsontext.Value(raw)
	if err := canonical.Format(jsontext.ReorderRawObjects(true)); err != nil {
		return nil, fmt.Errorf("canonicalize patched schema: %w", err)
	}
	out.SchemaHash = fmt.Sprintf("%x", sha256.Sum256(canonical))
	return &out, nil
}

func viewMembers(view manifest.View) (map[string]any, error) {
	raw, err := json.Marshal(view)
	if err != nil {
		return nil, fmt.Errorf("encode view: %w", err)
	}
	var members map[string]any
	if err := json.Unmarshal(raw, &members, json.WithUnmarshalers(viewNumberUnmarshaler)); err != nil {
		return nil, fmt.Errorf("decode view members: %w", err)
	}
	return members, nil
}

// Unmodeled view values can contain integers outside float64's exact range.
var viewNumberUnmarshaler = json.UnmarshalFromFunc(func(dec *jsontext.Decoder, value *any) error {
	if dec.PeekKind() != jsontext.KindNumber {
		return errors.ErrUnsupported
	}
	raw, err := dec.ReadValue()
	if err != nil {
		return err
	}
	*value = raw.Clone()
	return nil
})

func elementName(element map[string]any) string {
	field, _ := element["field"].(string)
	name, _ := element["name"].(string)
	label, _ := element["label"].(string)
	return cmp.Or(field, name, label)
}

// Only declaration collections contain overridable elements; option objects and
// component configuration can contain identical keys without being view elements.
var viewElementCollections = []string{"columns", "fields", "sections", "tabs"}

func visitViewElements(parent map[string]any, resource string, schema *registry.SchemaResponse, visit func(map[string]any, string)) {
	for _, key := range viewElementCollections {
		items, _ := parent[key].([]any)
		for _, item := range items {
			element, ok := item.(map[string]any)
			if !ok {
				continue
			}
			visit(element, resource)

			childResource := resource
			if element["type"] == "sub_list" {
				if field, ok := schemaField(schema, resource, elementName(element)); ok {
					childResource = field.RelatedModel
				}
			}
			visitViewElements(element, childResource, schema, visit)
		}
	}
}

func schemaField(schema *registry.SchemaResponse, resource, name string) (registry.SchemaField, bool) {
	for _, mod := range schema.Modules {
		for _, field := range mod.Models[resource].Fields {
			if field.Name == name {
				return field, true
			}
		}
	}
	return registry.SchemaField{}, false
}

func orderViewElements(parent map[string]any) {
	for _, key := range viewElementCollections {
		items, _ := parent[key].([]any)
		type orderedElement struct {
			item     any
			order    int
			explicit bool
		}
		ordered := make([]orderedElement, 0, len(items))
		hasOrder := false
		for index, item := range items {
			entry := orderedElement{item: item, order: index}
			if element, ok := item.(map[string]any); ok {
				orderViewElements(element)
				if order, ok := elementOrder(element); ok {
					entry.order = order
					entry.explicit = true
					hasOrder = true
				}
			}
			ordered = append(ordered, entry)
		}
		if !hasOrder {
			continue
		}
		slices.SortStableFunc(ordered, func(a, b orderedElement) int {
			if order := cmp.Compare(a.order, b.order); order != 0 {
				return order
			}
			if a.explicit != b.explicit {
				if a.explicit {
					return -1
				}
				return 1
			}
			return 0
		})
		for i, entry := range ordered {
			items[i] = entry.item
		}
	}
}

func elementOrder(element map[string]any) (int, bool) {
	switch value := element["order"].(type) {
	case int:
		return value, true
	case jsontext.Value:
		var order int
		err := json.Unmarshal(value, &order)
		return order, err == nil
	default:
		return 0, false
	}
}

func validateViewOverrideState(schema *registry.SchemaResponse, rows []viewOverrideResponse, viewName, target string) *resourceError {
	patches := overridesForView(rows, viewName)
	for _, mod := range schema.Modules {
		for _, view := range mod.Views {
			if view.Name != viewName {
				continue
			}
			members, err := viewMembers(view)
			if err != nil {
				return &resourceError{http.StatusInternalServerError, "internal_error", "validate view override failed"}
			}
			var failure *resourceError
			visitViewElements(members, view.Resource, schema, func(element map[string]any, resource string) {
				if elementName(element) != target {
					return
				}
				field, ok := schemaField(schema, resource, target)
				if !ok {
					return
				}
				hidden, _ := element["hidden"].(bool)
				required, _ := element["required"].(bool)
				required = required || field.Required
				if value, ok := patches[target]["hidden"].(bool); ok {
					hidden = value
				}
				if value, ok := patches[target]["required"].(bool); ok {
					required = value
				}
				if hidden && required {
					failure = invalidResourceRequest(fmt.Sprintf("field %q cannot be hidden while required", target))
				}
			})
			if failure != nil {
				return failure
			}
		}
	}
	return nil
}
