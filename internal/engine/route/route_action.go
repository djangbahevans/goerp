package route

import (
	"fmt"
	"slices"
	"strings"

	"github.com/djangbahevans/goerp/sdk/go/model"
)

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
		for _, name := range actionModelNames(moduleName, md) {
			declared[name] = md
		}
	}

	resolved := slices.Clone(explicit)
	for i, r := range resolved {
		if !isActionRoute(r) {
			continue
		}
		md, ok := declared[r.Model]
		if !ok {
			return nil, fmt.Errorf("route: module %q: action %q names model %q, which the module does not declare", moduleName, r.Name, r.Model)
		}
		resolved[i].Method, resolved[i].Path = deriveCRUDPath(md, model.Op{Name: r.Name})
	}
	return resolved, nil
}

func isActionRoute(r ExplicitRoute) bool {
	return r.Name != ""
}

// actionModelNames lists the names an engine.Action may use for md: its
// module-qualified name, plus md.Name itself when the declaration is
// already qualified with the module name (go-sdk-reference.md §22 declares
// models as "sales.order").
func actionModelNames(moduleName string, md model.ModelDeclaration) []string {
	names := []string{moduleName + "." + md.Name}
	if strings.HasPrefix(md.Name, moduleName+".") {
		names = append(names, md.Name)
	}
	return names
}

func actionClaimed(claimed map[actionIdentity]bool, moduleName string, md model.ModelDeclaration, action string) bool {
	for _, name := range actionModelNames(moduleName, md) {
		if claimed[actionIdentity{name, action}] {
			return true
		}
	}
	return false
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
