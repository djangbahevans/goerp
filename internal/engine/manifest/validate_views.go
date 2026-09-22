package manifest

import (
	"errors"
	"fmt"
	"strings"
)

// validateViews enforces the one view-level rule goerp#888 needs ahead of
// the common view-field validator goerp#885 will add alongside it: a
// `"custom"`-type view (view-system.md §9 "Custom views and components")
// must declare a non-empty `component` — the shell has nothing to render
// as the page body otherwise.
func validateViews(m Manifest) error {
	var violations []string

	for _, view := range m.Views {
		if view.Type == "custom" && view.Component == "" {
			violations = append(violations, fmt.Sprintf("view %q: type \"custom\" requires a non-empty component", view.Name))
		}
	}

	if len(violations) == 0 {
		return nil
	}
	return errors.New(strings.Join(violations, "; "))
}
