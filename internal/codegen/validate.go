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

// Validate checks every view in in.Views against in.Catalog
// (typescript-sdk-reference.md §16 "Code generation"): the ops its type
// and its create/update/delete actions need must be supported by its
// resource, and every "route" action must name a declared action. Each of
// in.ActionRefs must name a declared action too. It reports every problem,
// each naming the view or source location and the resource, op or action.
// A reference into a module that isn't in the catalog is not checked.
func Validate(in *Input) error {
	var errs []error
	for _, v := range in.Views {
		errs = append(errs, validateView(in.Catalog, v)...)
	}
	for _, ref := range in.ActionRefs {
		if err := checkActionRoute(in.Catalog, ref.Route); err != nil {
			errs = append(errs, fmt.Errorf("%s:%d: %w", ref.File, ref.Line, err))
		}
	}
	return errors.Join(errs...)
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
// routes, workflow transitions included. A route bound to one of the
// module's models through a CRUD action (a reserved-name engine.Action
// override, or engine.Model on a raw route) adds that op.
func catalogFor(models []Model, routes []Route) *CatalogModule {
	c := &CatalogModule{Ops: map[string]map[string]bool{}, Actions: map[string]bool{}}
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
		if r.Model != "" && r.CRUDAction != "" && c.Ops[r.Model] != nil {
			c.Ops[r.Model][r.CRUDAction] = true
		}
	}
	return c
}
