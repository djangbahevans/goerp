package loader

import (
	"cmp"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/djangbahevans/goerp/internal/engine/manifest"
	"github.com/djangbahevans/goerp/internal/engine/module"
	"github.com/djangbahevans/goerp/internal/engine/route"
)

// ValidateModuleSet runs declaration checks before publication. Repeated passes propagate
// dependency failures and refresh warnings against the surviving module set.
func ValidateModuleSet(modules map[string]*module.LoadedModule) {
	for {
		before := failedCount(modules)
		validateModuleDeclarations(modules)
		ValidateModelExtensions(modules)
		ValidateEventSubscriptions(modules)
		ValidateUsesConfig(modules)
		ValidateUsesPermissions(modules)
		applied := ValidateViewExtensions(modules)
		validateModelReferences(modules)

		if failedCount(modules) == before {
			LogViewExtensionConflicts(applied)
			return
		}
	}
}

// ValidateCandidate checks immutable copies of published modules. A replacement that
// invalidates a dependent is rejected along with its own invalid declarations.
func ValidateCandidate(candidate *module.LoadedModule, existing map[string]*module.LoadedModule) (map[string]*module.LoadedModule, error) {
	modules := make(map[string]*module.LoadedModule, len(existing)+1)
	order := candidate.LoadOrder

	for name, m := range existing {
		if name == candidate.Manifest.Name {
			continue
		}

		copyModule := *m
		copyModule.Diagnostics = slices.Clone(m.Diagnostics)
		modules[name] = &copyModule
		order = max(order, m.LoadOrder+1)
	}

	candidateCopy := *candidate
	candidateCopy.Diagnostics = slices.Clone(candidate.Diagnostics)
	candidateCopy.LoadOrder = order
	modules[candidate.Manifest.Name] = &candidateCopy

	ValidateModuleSet(modules)

	for _, name := range slices.Sorted(maps.Keys(modules)) {
		m := modules[name]
		old := existing[name]
		if m.Status == module.StatusFailed && (name == candidate.Manifest.Name || old != nil && old.Status != module.StatusFailed) {
			return nil, fmt.Errorf("module %q: %s", name, m.FailureReason)
		}
	}

	candidate.Diagnostics = candidateCopy.Diagnostics
	candidate.UsesConfig = candidateCopy.UsesConfig
	modules[candidate.Manifest.Name] = candidate

	return modules, nil
}

func failedCount(modules map[string]*module.LoadedModule) int {
	count := 0

	for _, m := range modules {
		if m.Status == module.StatusFailed {
			count++
		}
	}

	return count
}

func validationOrder(modules map[string]*module.LoadedModule) []string {
	names := slices.Sorted(maps.Keys(modules))
	slices.SortStableFunc(names, func(a, b string) int {
		return cmp.Compare(modules[a].LoadOrder, modules[b].LoadOrder)
	})
	return names
}

func validateModuleDeclarations(modules map[string]*module.LoadedModule) {
	models := make(map[string]string)
	providers := make(map[string]string)
	permissions := make(map[string]string)
	table := route.New()
	incumbents := make(map[string]*module.LoadedModule)

	for _, name := range validationOrder(modules) {
		m := modules[name]
		if m.Status == module.StatusFailed {
			continue
		}

		err := checkModuleDeclarations(m, modules, incumbents, models, providers, permissions)
		if err == nil {
			_, err = route.RegisterRoutes(table, name, m.Manifest.Type, route.ExplicitRoutesFrom(m.ExplicitRoutes), m.ModelDecls)
		}
		if err != nil {
			m.Fail(err.Error())
			continue
		}

		incumbents[name] = m
		for _, owned := range m.Manifest.Schema.OwnedModels {
			models[owned] = name
		}
		for _, permission := range m.Manifest.Permissions {
			permissions[permission.Name] = name
		}
		if provider := m.Manifest.OAuthProvider; provider != nil {
			providers[provider.Name] = name
		}
	}
}

func checkModuleDeclarations(m *module.LoadedModule, modules, incumbents map[string]*module.LoadedModule, models, providers, permissions map[string]string) error {
	mf := m.Manifest

	for _, dependency := range mf.DependsOn {
		if dep := modules[dependency]; dep == nil || dep.Status == module.StatusFailed {
			return fmt.Errorf("depends_on: module %q is not loaded", dependency)
		}
	}

	for _, name := range slices.Sorted(maps.Keys(incumbents)) {
		other := incumbents[name]
		if name != mf.Name && other.Status != module.StatusFailed && (slices.Contains(mf.ConflictsWith, name) || slices.Contains(other.Manifest.ConflictsWith, mf.Name)) {
			return fmt.Errorf("conflicts_with: module %q conflicts with loaded module %q", mf.Name, name)
		}
	}

	for _, owned := range mf.Schema.OwnedModels {
		if owner, found := models[owned]; found {
			return fmt.Errorf("schema.owned_models: model %q is already owned by module %q", owned, owner)
		}
	}

	if provider := mf.OAuthProvider; provider != nil {
		if owner, found := providers[provider.Name]; found {
			return fmt.Errorf("oauth_provider.name: %q is already declared by module %q", provider.Name, owner)
		}
	}

	if owner, name, found := FindPermissionCollision(permissions, mf.Permissions); found {
		return fmt.Errorf("permission %q already declared by module %q", name, owner)
	}

	for _, override := range mf.ReportOverrides {
		owner, report, qualified := strings.Cut(override.Overrides, ".")
		target := modules[owner]
		if !qualified || target == nil || target.Status == module.StatusFailed || !slices.ContainsFunc(target.Manifest.Reports, func(r manifest.Report) bool { return r.Name == report }) {
			return fmt.Errorf("report_overrides: target %q does not name a declared report", override.Overrides)
		}
	}

	return nil
}
