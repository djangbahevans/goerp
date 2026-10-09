package loader

import (
	"errors"

	"github.com/djangbahevans/goerp/internal/engine/modelextension"
	"github.com/djangbahevans/goerp/internal/engine/module"
)

func ValidateModelExtensions(modules map[string]*module.LoadedModule) {
	names, err := modelextension.Order(modules)
	if err != nil {
		for _, m := range modules {
			if len(m.ModelExtensions) > 0 {
				m.Fail(err.Error())
			}
		}
		return
	}

	validated := make(map[string]*module.LoadedModule, len(modules))
	for name, m := range modules {
		if len(m.ModelExtensions) == 0 && m.Manifest.Type != "field_extension" {
			validated[name] = m
		}
	}

	for _, name := range names {
		m := modules[name]
		if m.Status == module.StatusFailed {
			continue
		}
		for _, dependency := range m.Manifest.DependsOn {
			if dep := modules[dependency]; dep != nil && dep.Status == module.StatusFailed {
				m.FailDependency(dependency)
				break
			}
		}
		if m.Status == module.StatusFailed || len(m.ModelExtensions) == 0 && m.Manifest.Type != "field_extension" {
			continue
		}
		validated[name] = m
		if err := validateResolvedModels(validated); err != nil {
			m.Fail(err.Error())
		}
	}
}

// ValidateModelExtensionCandidate checks replacements against immutable copies of
// live modules, so a rejected load cannot change the published snapshot.
func ValidateModelExtensionCandidate(candidate *module.LoadedModule, existing map[string]*module.LoadedModule) error {
	modules := make(map[string]*module.LoadedModule, len(existing)+1)
	for name, m := range existing {
		copyModule := *m
		modules[name] = &copyModule
	}
	modules[candidate.Manifest.Name] = candidate

	if err := validateResolvedModels(modules); err != nil {
		return err
	}
	ValidateUsesPermissions(modules)
	if candidate.Status == module.StatusFailed {
		return errors.New(candidate.FailureReason)
	}

	return nil
}

func validateResolvedModels(modules map[string]*module.LoadedModule) error {
	resolved, err := modelextension.Resolve(modules)
	if err != nil {
		return err
	}

	for _, m := range resolved {
		if m.Status == module.StatusFailed {
			continue
		}

		if err := validateTrackedFields(m.ModelDecls); err != nil {
			return err
		}

		if err := validateSequenceFormats(m.ModelDecls); err != nil {
			return err
		}
	}

	return nil
}
