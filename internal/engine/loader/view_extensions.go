package loader

import (
	"fmt"
	"slices"
	"strings"

	"github.com/djangbahevans/goerp/internal/engine/manifest"
	"github.com/djangbahevans/goerp/internal/engine/module"
	"github.com/rs/zerolog/log"
)

// viewExtensionAreaNames maps a ViewExtensionDef.Type to the fixed
// target_section keyword and the view type it applies to
// (manifest-spec.md §11 ViewExtensionDef's target_section field: "For
// tabs: 'tabs'. For sections: 'sections'. ... For columns/filters/actions:
// the respective area name"). "fields" is deliberately absent — its
// target_section names an actual FormSection, resolved against the target
// view's declared sections instead of a fixed keyword.
var viewExtensionAreaNames = map[string]struct{ section, viewType string }{
	"tab":         {"tabs", "form"},
	"section":     {"sections", "form"},
	"columns":     {"columns", "list"},
	"filter":      {"filters", "list"},
	"action":      {"actions", "list"},
	"bulk_action": {"bulk_actions", "list"},
}

// ValidateViewExtensions checks every loaded module's view_extensions
// against the loaded module set's actual views (manifest-spec.md §11
// "Extension rules", §28 hard-error/warning rows; view-system.md §10
// "Extension restrictions"; engine-internals.md §2 Stage 3 step 23):
//
//   - An extension whose target module is in soft_depends_on and not
//     loaded is skipped silently — no error, no warning.
//   - An extension whose target module is loaded but declares no view
//     named in `extends` fails the extending module's load.
//   - A "fields"-type extension whose target_section resolves to a
//     sub_list section of the target form fails the extending module's
//     load.
//   - A target_section that names no section or area of the target view
//     logs a warning and the extension is skipped.
//
// The target module's views are its Manifest.Views as already merged by
// LoadModule (route.SynthesizeViews' EnableViews-derived views appended
// in), so no separate EnableViews resolution is needed here.
//
// Exported so a caller loading modules one at a time (not via LoadAll)
// can still run this same validation once its own loop finishes, the
// same pattern as ValidateEventSubscriptions.
func ValidateViewExtensions(modules map[string]*module.LoadedModule) {
	for name, m := range modules {
		if m.Status == module.StatusFailed {
			continue
		}

		defs := make(map[string]manifest.ViewExtensionDef, len(m.Manifest.ViewExtensionDefinitions))
		for _, def := range m.Manifest.ViewExtensionDefinitions {
			defs[def.Name] = def
		}

		for _, ref := range m.Manifest.ViewExtensions {
			targetModule, viewName, ok := strings.Cut(ref.Extends, ".")
			if !ok {
				continue // malformed extends is already a manifest-validation hard error
			}

			def, ok := defs[ref.Extension]
			if !ok {
				continue // unresolved extension name is already a manifest-validation hard error
			}

			target, loaded := modules[targetModule]
			if !loaded || target.Status == module.StatusFailed {
				if slices.Contains(m.Manifest.SoftDependsOn, targetModule) {
					continue
				}
				// A hard dependency that isn't loaded would already have
				// cascaded this module to StatusFailed before this runs
				// (engine-internals.md §2's dependency-failure
				// propagation), so this is unreachable in practice —
				// guarded defensively rather than assumed.
				m.Fail(fmt.Sprintf("view_extensions: extends %q targets module %q, which is not loaded", ref.Extends, targetModule))
				break
			}

			view, ok := findView(target.Manifest.Views, viewName)
			if !ok {
				m.Fail(fmt.Sprintf("view_extensions: extends %q: module %q declares no view named %q", ref.Extends, targetModule, viewName))
				break
			}

			if def.Type == "fields" {
				section, ok := findSection(view, def.TargetSection)
				switch {
				case !ok:
					log.Warn().Str("module", name).Str("extension", def.Name).Str("extends", ref.Extends).Str("target_section", def.TargetSection).
						Msg("view extension target_section not found on target view; extension skipped")
				case section.Type == "sub_list":
					m.Fail(fmt.Sprintf("view_extensions: extension %q: target_section %q on %q is a sub_list section — fields extensions cannot add fields to another module's sub_list", def.Name, def.TargetSection, ref.Extends))
				}
				if m.Status == module.StatusFailed {
					break
				}
				continue
			}

			area, known := viewExtensionAreaNames[def.Type]
			if !known || def.TargetSection != area.section || view.Type != area.viewType {
				log.Warn().Str("module", name).Str("extension", def.Name).Str("extends", ref.Extends).Str("target_section", def.TargetSection).
					Msg("view extension target_section not found on target view; extension skipped")
			}
		}
	}
}

// findView returns the view named name from views, if any.
func findView(views []manifest.View, name string) (manifest.View, bool) {
	for _, v := range views {
		if v.Name == name {
			return v, true
		}
	}
	return manifest.View{}, false
}

// findSection looks up a FormSection by name across a form view's
// top-level Sections and each of its Tabs' nested Sections — a "fields"
// extension's target_section may name either.
func findSection(view manifest.View, name string) (manifest.FormSection, bool) {
	if s, ok := findSectionIn(view.Sections, name); ok {
		return s, true
	}
	for _, tab := range view.Tabs {
		if s, ok := findSectionIn(tab.Sections, name); ok {
			return s, true
		}
	}
	return manifest.FormSection{}, false
}

func findSectionIn(sections []manifest.FormSection, name string) (manifest.FormSection, bool) {
	for _, s := range sections {
		if s.Name == name {
			return s, true
		}
	}
	return manifest.FormSection{}, false
}
