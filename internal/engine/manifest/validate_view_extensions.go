package manifest

import (
	"errors"
	"fmt"
	"slices"
	"strings"
)

// viewExtensionTypes are the only valid ViewExtensionDef.Type values
// (manifest-spec.md §11 ViewExtensionDef).
var viewExtensionTypes = []string{"tab", "section", "fields", "columns", "filter", "action", "bulk_action"}

// validateViewExtensions enforces manifest-spec.md §11's per-manifest
// view-extension rules: a `view_extensions[].extends` reference is
// well-formed and targets a declared dependency, `extension` names a
// definition this same manifest declares, definition names are unique,
// and each definition's `type`/`position`/`target_section`/payload shape
// is well-formed. Cross-module rules — whether the target view and
// target_section actually exist, and the soft-dependency skip — need the
// full loaded module set and live in internal/engine/loader instead
// (following ValidateEventSubscriptions), since a manifest is validated
// here in isolation, before any other module is loaded.
func validateViewExtensions(m Manifest) error {
	var violations []string
	reject := func(format string, args ...any) {
		violations = append(violations, fmt.Sprintf(format, args...))
	}

	defNames := make(map[string]int, len(m.ViewExtensionDefinitions))
	for _, def := range m.ViewExtensionDefinitions {
		defNames[def.Name]++
	}
	for name, count := range defNames {
		if count > 1 {
			reject("view_extension_definitions: name %q declared %d times, must be unique within the manifest", name, count)
		}
	}

	for _, ref := range m.ViewExtensions {
		module, ok := splitExtends(ref.Extends)
		if !ok {
			reject("view_extensions: extends %q must be {module}.{view_name}, each segment lowercase alphanumeric/underscore starting with a letter", ref.Extends)
			continue
		}

		if !slices.Contains(m.DependsOn, module) && !slices.Contains(m.SoftDependsOn, module) {
			reject("view_extensions: extends %q targets module %q, which is not in depends_on or soft_depends_on", ref.Extends, module)
		}

		if defNames[ref.Extension] == 0 {
			reject("view_extensions: extension %q does not name a definition in view_extension_definitions", ref.Extension)
		}
	}

	for _, def := range m.ViewExtensionDefinitions {
		if !slices.Contains(viewExtensionTypes, def.Type) {
			reject("view_extension_definitions %q: type %q must be one of %s", def.Name, def.Type, strings.Join(viewExtensionTypes, ", "))
		}

		if def.Position != "append" && def.Position != "prepend" {
			reject("view_extension_definitions %q: position %q must be \"append\" or \"prepend\"", def.Name, def.Position)
		}

		if def.TargetSection == "" {
			reject("view_extension_definitions %q: target_section must be non-empty", def.Name)
		}

		if err := validateExtensionPayload(def); err != nil {
			reject("view_extension_definitions %q: %v", def.Name, err)
		}
	}

	if len(violations) == 0 {
		return nil
	}
	return errors.New(strings.Join(violations, "; "))
}

// validateExtensionPayload checks that the payload field matching def.Type
// (manifest-spec.md §11 ViewExtensionDef's field table) is present. An
// unrecognized type has already been rejected by validateViewExtensions'
// own type check, so it's left unchecked here.
func validateExtensionPayload(def ViewExtensionDef) error {
	switch def.Type {
	case "tab":
		if def.Tab == nil {
			return errors.New("type \"tab\" requires a tab payload")
		}
	case "section":
		if def.Section == nil {
			return errors.New("type \"section\" requires a section payload")
		}
	case "fields":
		if len(def.Fields) == 0 {
			return errors.New("type \"fields\" requires a non-empty fields payload")
		}
	case "columns":
		if len(def.Columns) == 0 {
			return errors.New("type \"columns\" requires a non-empty columns payload")
		}
	case "filter":
		if def.Filter == nil {
			return errors.New("type \"filter\" requires a filter payload")
		}
	case "action":
		if def.Action == nil {
			return errors.New("type \"action\" requires an action payload")
		}
	case "bulk_action":
		if def.BulkAction == nil {
			return errors.New("type \"bulk_action\" requires a bulk_action payload")
		}
	}
	return nil
}

// splitExtends reports whether extends has exactly two `.`-delimited
// segments, each matching the same character class the manifest's own
// top-level `name` field is validated against (validNameRegex), and
// returns the first (module) segment when it does.
func splitExtends(extends string) (module string, ok bool) {
	mod, view, found := strings.Cut(extends, ".")
	if !found || !validNameRegex.MatchString(mod) || !validNameRegex.MatchString(view) {
		return "", false
	}
	return mod, true
}
