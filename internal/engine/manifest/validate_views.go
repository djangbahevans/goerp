package manifest

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
)

// validViewNameRegex is manifest-spec.md §9's "Alphanumeric and
// underscores" rule for a view's `name` — deliberately not validNameRegex
// (the module/permission/policy character class), which additionally
// requires a lowercase-letter start; the view name field's own spec text
// carries no such restriction.
var validViewNameRegex = regexp.MustCompile(`^[A-Za-z0-9_]+$`)

// validViewTypes is manifest-spec.md §9's `type` enum.
var validViewTypes = []string{"list", "form", "kanban", "calendar", "pivot", "timeline", "custom"}

// validateViews enforces manifest-spec.md §9's common view fields, shared
// by every views[] entry regardless of type: `name` (required, non-empty,
// alphanumeric/underscore, unique within the manifest), `type` (the fixed
// seven-value enum), `resource` (`{module}.{resource}`, each segment
// matching the manifest `name` character class), and `label` (required,
// non-empty) — plus goerp#888's type-specific rule that a `"custom"` view
// needs a non-empty `component`. Type-specific bodies (list columns, form
// sections, etc.) and cross-references (`row_click`, a view's `permission`
// naming a declared permission) are the per-view-type tickets' own scope,
// not checked here.
func validateViews(m Manifest) error {
	var violations []string
	reject := func(format string, args ...any) {
		violations = append(violations, fmt.Sprintf(format, args...))
	}

	seenNames := make(map[string]bool, len(m.Views))
	for _, view := range m.Views {
		if view.Name == "" || !validViewNameRegex.MatchString(view.Name) {
			reject("view %q: name is required and must be alphanumeric/underscore", view.Name)
		} else if seenNames[view.Name] {
			reject("view %q: name must be unique within this manifest's views", view.Name)
		} else {
			seenNames[view.Name] = true
		}

		if !slices.Contains(validViewTypes, view.Type) {
			reject("view %q: type %q must be one of %s", view.Name, view.Type, strings.Join(validViewTypes, ", "))
		}

		if _, ok := splitExtends(view.Resource); !ok {
			reject("view %q: resource %q must be {module}.{resource}, each segment lowercase alphanumeric/underscore starting with a letter", view.Name, view.Resource)
		}

		if view.Label == "" {
			reject("view %q: label is required", view.Name)
		}

		if view.Type == "custom" && view.Component == "" {
			reject("view %q: type \"custom\" requires a non-empty component", view.Name)
		}
	}

	if len(violations) == 0 {
		return nil
	}
	return errors.New(strings.Join(violations, "; "))
}

// A view's resource (manifest-spec.md §9's `{module}.{resource}` format) is
// the same shape splitExtends already parses for view_extensions[].extends
// (validate_view_extensions.go) — two `.`-delimited segments, each matching
// validNameRegex — so validateViews reuses it directly rather than
// reimplementing the same split-and-validate logic here.
