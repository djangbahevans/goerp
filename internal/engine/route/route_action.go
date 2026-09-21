package route

import (
	"cmp"
	"fmt"
	"maps"
	"slices"

	"github.com/djangbahevans/goerp/sdk/go/model"
)

const (
	scopeRecord       = "record"
	scopeCollection   = "collection"
	pathParamKindUUID = "uuid"
)

var actionMethods = []string{"GET", "POST", "PUT", "PATCH", "DELETE"}

type actionIdentity struct {
	model  string
	action string
}

// resolveActionRoutes fills in the method and module-relative path of every
// route registered through engine.Action, which the SDK declares by
// (model, action name) only. The path is derived from the module's own
// declaration of the model, so it is the same one EnableOps derives for the
// same reserved verb.
func resolveActionRoutes(moduleName string, explicit []ExplicitRoute, models []model.ModelDeclaration) ([]ExplicitRoute, error) {
	if !slices.ContainsFunc(explicit, isActionRoute) {
		return explicit, nil
	}

	declared := make(map[string]model.ModelDeclaration, len(models))
	for _, md := range models {
		declared[md.QualifiedName(moduleName)] = md
	}

	resolved := slices.Clone(explicit)
	seen := make(map[actionIdentity]bool, len(resolved))
	for i, r := range resolved {
		if !isActionRoute(r) {
			continue
		}
		md, ok := declared[r.Model]
		if !ok {
			return nil, fmt.Errorf("route: module %q: action %q names model %q, which the module does not declare", moduleName, r.Name, r.Model)
		}
		id := actionIdentity{r.Model, r.Name}
		if seen[id] {
			return nil, fmt.Errorf("route: module %q: action %q on model %q is registered more than once", moduleName, r.Name, r.Model)
		}
		seen[id] = true

		method, path, pathParams, err := deriveActionRoute(md, r)
		if err != nil {
			return nil, fmt.Errorf("route: module %q: action %q on model %q: %w", moduleName, r.Name, r.Model, err)
		}
		resolved[i].Method, resolved[i].Path, resolved[i].PathParams = method, path, pathParams
	}
	return resolved, nil
}

// deriveActionRoute derives an action's method, module-relative path and
// path parameters. A reserved name has a fixed method and path; a custom
// action is POST unless it declares a method, and addresses one record
// ({plural}/{id}/{name}, with a UUID id) unless its scope is collection.
func deriveActionRoute(md model.ModelDeclaration, r ExplicitRoute) (method, path string, pathParams map[string]string, err error) {
	if r.Scope != "" && r.Scope != scopeRecord && r.Scope != scopeCollection {
		return "", "", nil, fmt.Errorf("unknown scope %q", r.Scope)
	}

	if isReservedAction(r.Name) {
		if r.Method != "" {
			return "", "", nil, fmt.Errorf("the method of a reserved action is fixed, but %q was declared", r.Method)
		}
		method, path = deriveCRUDPath(md, model.Op{Name: r.Name})
		return method, path, r.PathParams, nil
	}

	method = cmp.Or(r.Method, "POST")
	if !slices.Contains(actionMethods, method) {
		return "", "", nil, fmt.Errorf("unsupported method %q", method)
	}
	plural := "/" + pluralPathSegment(md)
	if r.Scope == scopeCollection {
		return method, plural + "/" + r.Name, r.PathParams, nil
	}

	pathParams = maps.Clone(r.PathParams)
	if pathParams == nil {
		pathParams = map[string]string{}
	}
	if _, declared := pathParams["id"]; !declared {
		pathParams["id"] = pathParamKindUUID
	}
	return method, plural + "/{id}/" + r.Name, pathParams, nil
}

func isActionRoute(r ExplicitRoute) bool {
	return r.Name != ""
}

func isReservedAction(name string) bool {
	switch name {
	case model.List.Name, model.Get.Name, model.Create.Name, model.Update.Name,
		model.Delete.Name, model.Preview.Name, model.Pivot.Name:
		return true
	default:
		return false
	}
}

func actionClaimed(claimed map[actionIdentity]bool, moduleName string, md model.ModelDeclaration, action string) bool {
	return claimed[actionIdentity{md.QualifiedName(moduleName), action}]
}

// explicitActionIdentities lists the (model, action name) of every
// engine.Action route the module has already registered into table, so an
// EnableOps or workflow-transition candidate for the same identity is
// recognized as overridden without comparing paths.
func explicitActionIdentities(table *RouteTable, moduleName string) map[actionIdentity]bool {
	claimed := make(map[actionIdentity]bool)
	for _, r := range table.All() {
		m := r.Entry.Manifest
		if r.Entry.ModuleName == moduleName && !m.EngineNative && m.Name != "" {
			claimed[actionIdentity{m.Model, m.Name}] = true
		}
	}
	return claimed
}
