package route

import (
	"fmt"
	"strings"

	"github.com/djangbahevans/goerp/sdk/go/model"
	"github.com/go-openapi/inflect"
)

// SuppressedRoute names an EnableOps-derived (model, op) candidate that
// was dropped because an explicit route already claimed the same
// method+path — RegisterModelRoutes must run after RegisterModuleRoutes
// for the same module so explicit routes are already committed into
// table, letting this collision check favor the explicit registration
// (go-sdk-reference.md §2a "Collision with a hand-registered action").
type SuppressedRoute struct {
	Model string
	Op    string
}

// RegisterModelRoutes derives and registers the CRUD routes each model's
// EnableOps declaration allowlists — the engine-native counterpart to
// RegisterModuleRoutes's explicit-route registration. A model with no
// EnableOps call contributes zero routes; this is an allowlist, not a
// default (sdk/go/model.ModelDeclaration.EnableOps's own doc comment).
//
// A collision against a route already in table when this function
// starts — an explicit engine.Action for a reserved name, or a route
// from a module already processed — never fails the module load: the
// existing registration wins outright, and the suppressed auto-derived
// candidate is returned for the caller to log a startup warning against
// (go-sdk-reference.md §2a). A collision between two of *this call's own*
// candidates (two models in the same module deriving the same method+path,
// e.g. a duplicate LabelPlural) is a different case — nothing legitimate
// wins that one the way an explicit route legitimately overrides an
// auto-derived one, so it's a load-time error instead, the same way
// RegisterModuleRoutes errors on two explicit routes claiming the same
// path.
func RegisterModelRoutes(table *RouteTable, moduleName, moduleType string, models []model.ModelDeclaration) ([]SuppressedRoute, error) {
	var suppressed []SuppressedRoute
	claimedThisCall := make(map[string]string, len(models)) // "method path" -> qualified model that claimed it

	prefix := modulePathPrefix(moduleName, moduleType)

	for _, md := range models {
		qualifiedModel := moduleName + "." + md.Name

		for _, op := range md.EnabledOps {
			method, relPath := deriveCRUDPath(md, op)
			expandedPath := prefix + relPath
			key := method + " " + expandedPath

			if claimant, ok := claimedThisCall[key]; ok {
				return suppressed, fmt.Errorf("route: module %q: models %q and %q both derive %s %s from EnableOps",
					moduleName, claimant, qualifiedModel, method, expandedPath)
			}

			if table.Registered(method, expandedPath) {
				suppressed = append(suppressed, SuppressedRoute{Model: qualifiedModel, Op: op.Name})
				continue
			}

			table.Register(method, expandedPath, &RouteEntry{
				ModuleName:   moduleName,
				PathTemplate: expandedPath,
				Manifest: RouteManifest{
					Auth:           "required",
					Model:          qualifiedModel,
					ResponseIsList: op.Name == model.List.Name,
					CrudAction:     op.Name,
					EngineNative:   true,
					StorageBackend: storageBackendString(md.Backend),
				},
			})
			claimedThisCall[key] = qualifiedModel
		}
	}

	return suppressed, nil
}

// deriveCRUDPath derives the method and module-relative path for one
// model op, mirroring sdk/go/engine/action.go's actionPath exactly for
// the six reserved ops. Unlike that SDK-side function — which only has a
// bare model-name string, no model registry, and so can never reach
// LabelPlural — this has the full ModelDeclaration and implements the
// documented rule in full (go-sdk-reference.md §2a "Path and plural
// derivation"): RoutePrefixOverride wins outright if set (no
// pluralization applied, it's an explicit override); otherwise pluralize
// LabelPlural, or the model's bare resource segment if LabelPlural isn't
// set either — the same last-dotted-segment fallback actionPath's
// pluralSegment already uses, so the two stay byte-identical whenever
// LabelPlural/RoutePrefix are unset.
func deriveCRUDPath(md model.ModelDeclaration, op model.Op) (method, path string) {
	plural := "/" + pluralPathSegment(md)

	switch op.Name {
	case model.List.Name:
		return "GET", plural
	case model.Get.Name:
		return "GET", plural + "/{id}"
	case model.Create.Name:
		return "POST", plural
	case model.Update.Name:
		return "PUT", plural + "/{id}"
	case model.Delete.Name:
		return "DELETE", plural + "/{id}"
	case model.Preview.Name:
		return "POST", plural + "/preview"
	default:
		// EnabledOps is only ever populated from the six reserved model.Op
		// values (model.go's EnableOps doc comment) — no custom-action
		// shape exists at the model-declaration level the way
		// sdk/go/engine.Action's default case handles one.
		return "POST", plural + "/{id}/" + op.Name
	}
}

// pluralPathSegment implements go-sdk-reference.md §2a's documented path
// derivation rule in full, unlike sdk/go/engine's pluralSegment (which
// can't reach LabelPlural or RoutePrefixOverride — see that function's
// own doc comment).
func pluralPathSegment(md model.ModelDeclaration) string {
	if md.RoutePrefixOverride != "" {
		return strings.Trim(md.RoutePrefixOverride, "/")
	}

	label := md.LabelPlural
	if label == "" {
		label = bareNameSegment(md.Name)
	}

	return inflect.Parameterize(inflect.Pluralize(label))
}

// bareNameSegment returns the last dot-separated segment of a model
// name — the fallback used whenever a human-facing label derived from
// the model's own name isn't set (pluralPathSegment's LabelPlural
// fallback above; route_view.go's displayLabel).
func bareNameSegment(name string) string {
	if i := strings.LastIndex(name, "."); i >= 0 {
		return name[i+1:]
	}
	return name
}

// storageBackendString maps model.ModelBackend's zero-value-is-the-
// default convention (model.go's own doc comment) onto
// RouteManifest.StorageBackend's three explicit string values — an
// explicit switch rather than a bare cast, since the two types' "table"
// case don't share a literal representation ("" vs "table").
func storageBackendString(b model.ModelBackend) string {
	switch b {
	case model.BackendTransient:
		return "transient"
	case model.BackendVirtual:
		return "virtual"
	default:
		return "table"
	}
}

// modulePathPrefix mirrors RegisterModuleRoutes's own prefix computation
// (route_module.go) — factored out here so both explicit and
// EnableOps-derived routes expand against the identical module/connector
// prefix rule.
func modulePathPrefix(moduleName, moduleType string) string {
	if moduleType == "connector" {
		return "/connectors/" + moduleName
	}
	return "/" + moduleName
}

// RegisterRoutes registers one module's explicit routes and its models'
// EnableOps-derived candidates into table, in the one order this is ever
// safe to do (RegisterModuleRoutes first, so RegisterModelRoutes has
// something to suppress against). Every route-table-building call site —
// loader.LoadAll, moduleboot.LoadCascading, registry.buildRouteTable, and
// eventually hot reload's pre-flight merge check
// (engine-internals.md §10's mergeEnableOpsRoutes) — needs this exact
// pair; a shared entry point is what keeps them from drifting.
func RegisterRoutes(table *RouteTable, moduleName, moduleType string, explicit []ExplicitRoute, models []model.ModelDeclaration) ([]SuppressedRoute, error) {
	if err := RegisterModuleRoutes(table, moduleName, moduleType, explicit); err != nil {
		return nil, err
	}
	return RegisterModelRoutes(table, moduleName, moduleType, models)
}
