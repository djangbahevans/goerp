package codegen

import (
	"errors"
	"fmt"
	"strings"
)

// listShapedViews are the view types that render a resource's list
// dataset, and so need its list op.
var listShapedViews = map[string]bool{
	"list": true, "kanban": true, "calendar": true, "pivot": true, "timeline": true,
}

// actionOps maps a view action type to the op it needs on the view's
// resource.
var actionOps = map[string]string{
	"create": "create",
	"update": "update",
	"delete": "delete",
}

// Validate reports all invalid view references and frontend ref literals.
func Validate(in *Input) error {
	var errs []error
	for _, v := range in.Views {
		errs = append(errs, validateView(in.Catalog, v)...)
	}
	for _, ref := range in.NameRefs {
		if err := validateNameRef(in, ref); err != nil {
			errs = append(errs, fmt.Errorf("%s:%d: %w", ref.File, ref.Line, err))
		}
	}
	return errors.Join(errs...)
}

func validateNameRef(in *Input, ref NameRef) error {
	declares := func(module string, catalog *CatalogModule) bool {
		switch ref.Helper {
		case "resourceRef":
			_, ok := catalog.Ops[ref.Name]
			return ok
		case "actionRef":
			name, ok := strings.CutPrefix(ref.Name, module+".")
			return ok && catalog.Actions[name]
		case "permissionRef":
			return catalog.Permissions[ref.Name]
		}
		return false
	}

	if own := in.Catalog[in.Module]; own != nil && declares(in.Module, own) {
		return fmt.Errorf("%s(%q) names a declaration of module %s; use the registered name directly", ref.Helper, ref.Name, in.Module)
	}
	if !in.CompleteCatalog {
		return nil
	}
	for module, catalog := range in.Catalog {
		if declares(module, catalog) {
			return nil
		}
	}
	return fmt.Errorf("%s(%q) names no declaration in any loaded module", ref.Helper, ref.Name)
}

// checkActionRoute reports a route string that isn't "module.actionName",
// or names an action its module, when the catalog knows it, doesn't
// declare.
func checkActionRoute(catalog map[string]*CatalogModule, route string) error {
	module, name, ok := strings.Cut(route, ".")
	if !ok || module == "" || name == "" {
		return fmt.Errorf("route %q is not \"module.actionName\"", route)
	}
	if m, known := catalog[module]; known && !m.Actions[name] {
		return fmt.Errorf("route %q names no action: module %s declares no action named %q", route, module, name)
	}
	return nil
}

func validateView(catalog map[string]*CatalogModule, v View) []error {
	var errs []error

	if v.Resource != "" {
		ops, known := resourceOps(catalog, v.Resource)
		need := func(op, overrideRoute, why string) {
			if !known || overrideRoute != "" || ops[op] {
				return
			}
			errs = append(errs, fmt.Errorf("view %q: resource %s has no %q op, which %s needs", v.Name, v.Resource, op, why))
		}
		switch {
		case listShapedViews[v.Type]:
			need("list", v.FetchRoute, "a "+v.Type+" view")
		case v.Type == "form":
			need("get", v.FetchRoute, "a form view")
		}
		for _, a := range v.Actions {
			op, ok := actionOps[a.Type]
			if !ok {
				continue
			}
			override := map[string]string{"create": v.CreateRoute, "update": v.UpdateRoute, "delete": v.DeleteRoute}[op]
			need(op, override, fmt.Sprintf("its %q action %q", a.Type, a.Label))
		}
	}

	for _, a := range v.Actions {
		if a.Type != "route" {
			continue
		}
		if err := checkActionRoute(catalog, a.Route); err != nil {
			errs = append(errs, fmt.Errorf("view %q (resource %s): action %q: %w", v.Name, v.Resource, a.Label, err))
		}
	}
	return errs
}

// resourceOps returns the ops qualified's model supports, and false when
// the catalog doesn't know its module. A module the catalog knows that
// doesn't declare the model returns an empty op set, so every op it needs
// fails.
func resourceOps(catalog map[string]*CatalogModule, qualified string) (map[string]bool, bool) {
	module, _, ok := strings.Cut(qualified, ".")
	if !ok {
		return nil, false
	}
	m, known := catalog[module]
	if !known {
		return nil, false
	}
	return m.Ops[qualified], true
}

// catalogFor builds the catalog entry for one module from its models and
// routes, workflow transitions included. Reserved-name engine.DefineAction
// overrides add ops; raw engine.Model bindings only govern field security.
func catalogFor(models []Model, routes []Route) *CatalogModule {
	c := &CatalogModule{
		Ops:         map[string]map[string]bool{},
		Actions:     map[string]bool{},
		Permissions: map[string]bool{},
	}
	for _, m := range models {
		ops := map[string]bool{}
		for _, op := range m.Ops {
			ops[op] = true
		}
		c.Ops[m.Name] = ops
	}
	for _, r := range routes {
		if r.Name != "" {
			c.Actions[r.Name] = true
		}
		if r.Model != "" && r.CRUDAction != "" && r.Name == r.CRUDAction && c.Ops[r.Model] != nil {
			c.Ops[r.Model][r.CRUDAction] = true
		}
	}
	return c
}
