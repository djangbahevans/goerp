package route

import (
	"fmt"
	"strings"

	"github.com/djangbahevans/goerp/sdk/go/model"
	"github.com/go-openapi/inflect"
)

// SuppressedRoute identifies a derived route shadowed by an explicit registration.
type SuppressedRoute struct {
	Model string
	Op    string
	// Kind distinguishes CRUD operations from workflow transitions in startup warnings.
	Kind string
}

const SuppressedWorkflowTransition = "workflow_transition"

// LogMessage describes the suppressed route for a startup warning.
func (s SuppressedRoute) LogMessage() string {
	if s.Kind == SuppressedWorkflowTransition {
		return "Workflow: explicit route already registered, auto-derived transition action suppressed"
	}
	return "EnableOps: explicit route already registered, auto-derived route suppressed"
}

// RegisterModelRoutes registers the operations each model enables. Explicit routes must
// be registered first; they suppress derived routes. Conflicting derived paths fail.
func RegisterModelRoutes(table *RouteTable, moduleName, moduleType string, models []model.ModelDeclaration) ([]SuppressedRoute, error) {
	var suppressed []SuppressedRoute
	claimedThisCall := make(map[string]string, len(models))

	prefix := ModulePathPrefix(moduleName, moduleType)
	claimedActions := explicitActionIdentities(table, moduleName)

	for _, md := range models {
		qualifiedModel := md.QualifiedName(moduleName)

		for _, op := range md.EnabledOps {
			method, relPath := deriveCRUDPath(md, op)
			expandedPath := prefix + relPath
			key := method + " " + expandedPath

			if claimant, ok := claimedThisCall[key]; ok {
				return suppressed, fmt.Errorf("route: module %q: models %q and %q both derive %s %s from EnableOps",
					moduleName, claimant, qualifiedModel, method, expandedPath)
			}

			if actionClaimed(claimedActions, moduleName, md, op.Name) || table.Registered(method, expandedPath) {
				suppressed = append(suppressed, SuppressedRoute{Model: qualifiedModel, Op: op.Name})
				continue
			}

			var permissions []string
			if op.Permission != "" {
				permissions = []string{op.Permission}
			}

			table.Register(method, expandedPath, &RouteEntry{
				ModuleName:   moduleName,
				PathTemplate: expandedPath,
				Manifest: RouteManifest{
					Auth:           "required",
					Permissions:    permissions,
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

// RegisterModelWorkflowActions registers declared workflow transitions. Explicit
// actions and routes suppress matching derived transitions.
func RegisterModelWorkflowActions(table *RouteTable, moduleName, moduleType string, models []model.ModelDeclaration) ([]SuppressedRoute, error) {
	var suppressed []SuppressedRoute
	claimedThisCall := make(map[string]string, len(models))

	prefix := ModulePathPrefix(moduleName, moduleType)
	claimedActions := explicitActionIdentities(table, moduleName)

	for _, md := range models {
		qualifiedModel := md.QualifiedName(moduleName)
		plural := "/" + pluralPathSegment(md)

		for _, f := range md.Fields {
			for _, t := range f.Def.WorkflowTransitions {
				method, relPath := "POST", plural+"/{id}/"+t.ActionName
				expandedPath := prefix + relPath
				key := method + " " + expandedPath

				if claimant, ok := claimedThisCall[key]; ok {
					return suppressed, fmt.Errorf("route: module %q: models %q and %q both derive %s %s from a workflow transition",
						moduleName, claimant, qualifiedModel, method, expandedPath)
				}

				if actionClaimed(claimedActions, moduleName, md, t.ActionName) || table.Registered(method, expandedPath) {
					suppressed = append(suppressed, SuppressedRoute{Model: qualifiedModel, Op: t.ActionName, Kind: SuppressedWorkflowTransition})
					continue
				}

				var permissions []string
				if t.Permission != "" {
					permissions = []string{t.Permission}
				}

				table.Register(method, expandedPath, &RouteEntry{
					ModuleName:   moduleName,
					PathTemplate: expandedPath,
					Manifest: RouteManifest{
						Auth:           "required",
						Permissions:    permissions,
						Model:          qualifiedModel,
						Name:           t.ActionName,
						CrudAction:     "workflow_transition",
						EngineNative:   true,
						StorageBackend: storageBackendString(md.Backend),
						Workflow: &WorkflowManifest{
							Field:     f.Name,
							From:      t.From,
							To:        t.To,
							Condition: t.ConditionExpr,
							Event:     t.Event,
						},
					},
				})
				claimedThisCall[key] = qualifiedModel
			}
		}
	}

	return suppressed, nil
}

// deriveCRUDPath maps reserved verbs to their paths; other names are record actions.
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
	case model.Pivot.Name:
		return "GET", plural + "/pivot"
	default:
		return "POST", plural + "/{id}/" + op.Name
	}
}

// pluralPathSegment uses an explicit prefix or pluralizes the label or resource name.
func pluralPathSegment(md model.ModelDeclaration) string {
	if md.RoutePrefixOverride != "" {
		return strings.Trim(md.RoutePrefixOverride, "/")
	}

	label := md.LabelPlural
	if label == "" {
		label = md.ResourceName()
	}

	return inflect.Parameterize(inflect.Pluralize(label))
}

// The table backend uses an empty SDK value but an explicit wire value.
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

// ModulePathPrefix returns the module or connector URL prefix.
func ModulePathPrefix(moduleName, moduleType string) string {
	if moduleType == "connector" {
		return "/connectors/" + moduleName
	}
	return "/" + moduleName
}

// RegisterRoutes registers explicit routes before derived routes so overrides win.
func RegisterRoutes(table *RouteTable, moduleName, moduleType string, explicit []ExplicitRoute, models []model.ModelDeclaration) ([]SuppressedRoute, error) {
	explicit, err := resolveActionRoutes(moduleName, explicit, models)
	if err != nil {
		return nil, err
	}

	if err := RegisterModuleRoutes(table, moduleName, moduleType, explicit); err != nil {
		return nil, err
	}

	suppressed, err := RegisterModelRoutes(table, moduleName, moduleType, models)
	if err != nil {
		return nil, err
	}

	workflowSuppressed, err := RegisterModelWorkflowActions(table, moduleName, moduleType, models)
	if err != nil {
		return nil, err
	}

	return append(suppressed, workflowSuppressed...), nil
}
