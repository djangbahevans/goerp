package loader

import (
	"fmt"
	"slices"
	"strings"

	"github.com/djangbahevans/goerp/internal/engine/manifest"
	"github.com/djangbahevans/goerp/internal/engine/module"
	"github.com/rs/zerolog/log"
)

// Fixed extension areas map to view types. The fields area instead names a declared form
// section and must resolve against the target view.
var viewExtensionAreaNames = map[string]struct{ section, viewType string }{
	"tab":         {"tabs", "form"},
	"section":     {"sections", "form"},
	"columns":     {"columns", "list"},
	"filter":      {"filters", "list"},
	"action":      {"actions", "list"},
	"bulk_action": {"bulk_actions", "list"},
}

// AppliedViewExtension identifies an extension whose target view and section resolved
// successfully.
type AppliedViewExtension struct {
	Module        string // the extending module
	LoadOrder     int    // the extending module's dependency-load order
	TargetView    string // "{module}.{view_name}", i.e. ref.Extends
	TargetSection string
	Position      string
	Type          string
	// TabLabel is the tab's label when Type == "tab", "" otherwise —
	// LogViewExtensionConflicts' log line names the colliding tabs by
	// label (view-system.md §17 "View extension conflict detection").
	TabLabel string
}

// ValidateViewExtensions returns applicable extensions for conflict checks. Absent soft
// dependencies are skipped; missing views or sub-list field targets fail loading, while
// unresolved sections warn and skip the extension.
func ValidateViewExtensions(modules map[string]*module.LoadedModule) []AppliedViewExtension {
	var applied []AppliedViewExtension

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
				default:
					applied = append(applied, AppliedViewExtension{
						Module: name, LoadOrder: m.LoadOrder, TargetView: ref.Extends,
						TargetSection: def.TargetSection, Position: def.Position, Type: def.Type,
					})
				}
				if m.Status == module.StatusFailed {
					break
				}
				continue
			}

			area, known := viewExtensionAreaNames[def.Type]
			switch {
			case !known || def.TargetSection != area.section:
				log.Warn().Str("module", name).Str("extension", def.Name).Str("extends", ref.Extends).Str("target_section", def.TargetSection).
					Msg("view extension target_section not found on target view; extension skipped")
			case view.Type != area.viewType:
				log.Warn().Str("module", name).Str("extension", def.Name).Str("extends", ref.Extends).Str("target_section", def.TargetSection).Str("view_type", view.Type).
					Msg("view extension target_section names the right area, but the target view is the wrong type for it; extension skipped")
			default:
				tabLabel := ""
				if def.Type == "tab" && def.Tab != nil {
					tabLabel = def.Tab.Label
				}
				applied = append(applied, AppliedViewExtension{
					Module: name, LoadOrder: m.LoadOrder, TargetView: ref.Extends,
					TargetSection: def.TargetSection, Position: def.Position, Type: def.Type, TabLabel: tabLabel,
				})
			}
		}
	}

	return applied
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
