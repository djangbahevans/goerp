package manifest

import (
	"errors"
	"fmt"
	"strings"
)

// moduleHasUI reports whether m presents any UI — either a generic-renderer
// View (manifest-spec.md §9) or a custom FrontendConfig bundle (§2's
// "frontend" field) — the two ways a module can have UI.
func moduleHasUI(m Manifest) bool {
	return len(m.Views) > 0 || m.Frontend != nil
}

// validateModuleType enforces the per-type manifest-field constraint matrix
// (manifest-spec.md §3 "Type-specific validation rules"): wasm requirement,
// has_ui, owns_schema, and each type's required extra fields. All of it is
// checkable from the parsed manifest alone.
//
// The matrix's "may register routes" column is not enforced here — that
// needs a get_routes() WASM call the loader makes, not the manifest package
// (goerp#118). The connector type's owns_schema column is skipped entirely
// for the same reason: its "forbidden" rule carves out model.Virtual()
// models, and only a get_model_declarations() call can tell those apart
// from table-backed ones — enforcing "forbidden" without that carve-out
// would reject legitimate connector modules (goerp#116).
func validateModuleType(m Manifest) error {
	var violations []string
	reject := func(format string, args ...any) {
		violations = append(violations, fmt.Sprintf(format, args...))
	}

	ownsSchema := len(m.Schema.OwnedModels) > 0
	hasUI := moduleHasUI(m)

	requireWasm := func() {
		if !m.Wasm {
			reject("type %q requires wasm: true", m.Type)
		}
	}
	forbidOwnsSchema := func() {
		if ownsSchema {
			reject("type %q must not declare schema.owned_models", m.Type)
		}
	}
	forbidUI := func() {
		if hasUI {
			reject("type %q must not declare views or a frontend bundle", m.Type)
		}
	}

	switch m.Type {
	case "domain":
		requireWasm()

	case "l10n":
		forbidOwnsSchema()

	case "connector":
		requireWasm()

	case "bridge":
		requireWasm()
		forbidOwnsSchema()
		forbidUI()
		if len(m.DependsOn) < 2 {
			reject("type %q requires depends_on length >= 2, got %d", m.Type, len(m.DependsOn))
		}

	case "theme":
		if m.Wasm {
			reject("type %q requires wasm: false", m.Type)
		}
		forbidOwnsSchema()
		if !hasUI {
			reject("type %q requires a view or a frontend bundle", m.Type)
		}

	case "report_bundle":
		forbidOwnsSchema()
		forbidUI()
		if len(m.Reports) < 1 {
			reject("type %q requires reports length >= 1, got %d", m.Type, len(m.Reports))
		}

	case "automation":
		requireWasm()
		forbidOwnsSchema()
		forbidUI()
		if len(m.Subscribes) < 1 {
			reject("type %q requires subscribes length >= 1, got %d", m.Type, len(m.Subscribes))
		}

	case "field_extension":
		forbidOwnsSchema()
		if len(m.ViewExtensions) == 0 && len(m.ViewExtensionDefinitions) == 0 {
			reject("type %q requires view_extensions or view_extension_definitions", m.Type)
		}
		if m.Schema.ExtendsModule == nil || *m.Schema.ExtendsModule == "" {
			reject("type %q requires schema.extends_module", m.Type)
		}
		if len(m.Schema.ExtendsModels) == 0 {
			reject("type %q requires schema.extends_models", m.Type)
		}
	}

	if len(violations) == 0 {
		return nil
	}
	return errors.New(strings.Join(violations, "; "))
}
