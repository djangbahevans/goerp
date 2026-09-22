package loader

import (
	"cmp"
	"fmt"
	"slices"

	"github.com/rs/zerolog/log"
)

// viewExtensionLocation is the (target view, target_section, position)
// triple LogViewExtensionConflicts groups AppliedViewExtension entries by —
// two extensions sharing all three land in the same spot in the rendered
// view, whether or not they came from the same module.
type viewExtensionLocation struct {
	view, section, position string
}

// LogViewExtensionConflicts groups applied (extensions ValidateViewExtensions
// returned as actually landing) by target view/target_section/position and
// logs a `view extension conflict` warning, per view-system.md §17 "View
// extension conflict detection", whenever a group spans more than one
// module — both extensions still apply; the warning only tells an operator
// which one a same-labelled UI element (if any) will show first. Within a
// group, extensions are ordered by dependency load order (lower applies
// first, per moduleboot.Order) and a warning is logged for each adjacent
// pair, so an N-module group produces N-1 warnings rather than one
// combinatorial blast — the doc's own example is the two-module case this
// collapses to.
func LogViewExtensionConflicts(applied []AppliedViewExtension) {
	byLocation := make(map[viewExtensionLocation][]AppliedViewExtension)
	for _, ext := range applied {
		key := viewExtensionLocation{view: ext.TargetView, section: ext.TargetSection, position: ext.Position}
		byLocation[key] = append(byLocation[key], ext)
	}

	for loc, group := range byLocation {
		slices.SortFunc(group, func(a, b AppliedViewExtension) int {
			return cmp.Or(cmp.Compare(a.LoadOrder, b.LoadOrder), cmp.Compare(a.Module, b.Module))
		})

		for i := 1; i < len(group); i++ {
			a, b := group[i-1], group[i]
			if a.Module == b.Module {
				continue // same module contributing twice to one location isn't a cross-module conflict
			}

			log.Warn().
				Str("view", loc.view).
				Str("section", loc.section).
				Str("position", loc.position).
				Str("module_a", moduleConflictLabel(a)).
				Str("module_b", moduleConflictLabel(b)).
				Str("resolution", fmt.Sprintf("%s applied first (%s has lower dependency order)", a.Module, a.Module)).
				Msg("view extension conflict")
		}
	}
}

// moduleConflictLabel is the module name, suffixed with its tab label —
// `hr (tab: "Employment")` — when the extension is a "tab", matching
// view-system.md §17's log example.
func moduleConflictLabel(ext AppliedViewExtension) string {
	if ext.Type == "tab" && ext.TabLabel != "" {
		return fmt.Sprintf("%s (tab: %q)", ext.Module, ext.TabLabel)
	}
	return ext.Module
}
