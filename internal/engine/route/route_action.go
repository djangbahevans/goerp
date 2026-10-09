package route

import (
	"cmp"
	"fmt"
	"maps"
	"slices"

	"github.com/djangbahevans/goerp/sdk/go/model"
)

const (
	scopeRecord       = "record"
	scopeCollection   = "collection"
	pathParamKindUUID = "uuid"
)

var actionMethods = []string{"GET", "POST", "PUT", "PATCH", "DELETE"}

type actionIdentity struct {
	model  string
	action string
}

// ValidateActionRoutes checks action identities and transition overrides against
// the model declarations before a module can finish loading.
func ValidateActionRoutes(moduleName string, explicit []ExplicitRoute, models []model.ModelDeclaration) error {
	_, err := resolveActionRoutes(moduleName, explicit, models)
	return err
}

// Action paths and transition guards come from model declarations, so overrides
// cannot drift from the routes they replace.
func resolveActionRoutes(moduleName string, explicit []ExplicitRoute, models []model.ModelDeclaration) ([]ExplicitRoute, error) {
	if !slices.ContainsFunc(explicit, func(r ExplicitRoute) bool { return isActionRoute(r) || r.Transition != nil }) {
		return explicit, nil
	}

	declared := make(map[string]model.ModelDeclaration, len(models))
	for _, md := range models {
		declared[md.QualifiedName(moduleName)] = md
	}

	resolved := slices.Clone(explicit)
	seen := make(map[actionIdentity]bool, len(resolved))
	for i, r := range resolved {
		if r.Transition != nil && r.Name == "" {
			return nil, fmt.Errorf("route: module %q: transition override on model %q requires an action name", moduleName, r.Model)
		}
		if !isActionRoute(r) {
			continue
		}
		md, ok := declared[r.Model]
		if !ok {
			return nil, fmt.Errorf("route: module %q: action %q names model %q, which the module does not declare", moduleName, r.Name, r.Model)
		}
		id := actionIdentity{r.Model, r.Name}
		if seen[id] {
			return nil, fmt.Errorf("route: module %q: action %q on model %q is registered more than once", moduleName, r.Name, r.Model)
		}
		seen[id] = true

		workflow, permission := modelWorkflow(md, r.Name)
		if workflow != nil && r.Transition == nil {
			return nil, fmt.Errorf("route: module %q: action %q on model %q shadows a workflow transition; register it through engine.HandleTransition", moduleName, r.Name, r.Model)
		}
		if r.Transition != nil {
			if workflow == nil || r.Transition.From != workflow.From || r.Transition.To != workflow.To {
				return nil, fmt.Errorf("route: module %q: action %q on model %q does not match a declared workflow transition", moduleName, r.Name, r.Model)
			}
			if r.Method != "" || r.Scope != "" || r.RequestType != nil {
				return nil, fmt.Errorf("route: module %q: action %q on model %q: a transition override is a POST record action without a request body", moduleName, r.Name, r.Model)
			}

			r.Auth = "required"
			r.Permissions = nil
			if permission != "" {
				r.Permissions = []string{permission}
			}
			r.Workflow = workflow
			r.CrudAction = "workflow_transition"
			r.StorageBackend = storageBackendString(md.Backend)
			r.ResponseIsList = false
		}

		method, path, pathParams, err := deriveActionRoute(md, r)
		if err != nil {
			return nil, fmt.Errorf("route: module %q: action %q on model %q: %w", moduleName, r.Name, r.Model, err)
		}
		r.Method, r.Path, r.PathParams = method, path, pathParams
		resolved[i] = r
	}
	return resolved, nil
}

func modelWorkflow(md model.ModelDeclaration, action string) (*WorkflowManifest, string) {
	for _, field := range md.Fields {
		for _, transition := range field.Def.WorkflowTransitions {
			if transition.ActionName == action {
				return &WorkflowManifest{
					Field:     field.Name,
					From:      transition.From,
					To:        transition.To,
					Condition: transition.ConditionExpr,
					Event:     transition.Event,
				}, transition.Permission
			}
		}
	}

	return nil, ""
}

func deriveActionRoute(md model.ModelDeclaration, r ExplicitRoute) (method, path string, pathParams map[string]string, err error) {
	if r.Scope != "" && r.Scope != scopeRecord && r.Scope != scopeCollection {
		return "", "", nil, fmt.Errorf("unknown scope %q", r.Scope)
	}

	if r.Transition == nil && isReservedAction(r.Name) {
		if r.Method != "" {
			return "", "", nil, fmt.Errorf("the method of a reserved action is fixed, but %q was declared", r.Method)
		}
		if r.Scope != "" {
			return "", "", nil, fmt.Errorf("the scope of a reserved action is fixed, but %q was declared", r.Scope)
		}
		method, path = deriveCRUDPath(md, model.Op{Name: r.Name})
		return method, path, r.PathParams, nil
	}

	method = cmp.Or(r.Method, "POST")
	if !slices.Contains(actionMethods, method) {
		return "", "", nil, fmt.Errorf("unsupported method %q", method)
	}
	plural := "/" + pluralPathSegment(md)
	if r.Scope == scopeCollection {
		return method, plural + "/" + r.Name, r.PathParams, nil
	}

	pathParams = maps.Clone(r.PathParams)
	if pathParams == nil {
		pathParams = map[string]string{}
	}
	if _, declared := pathParams["id"]; !declared {
		pathParams["id"] = pathParamKindUUID
	}
	return method, plural + "/{id}/" + r.Name, pathParams, nil
}

func isActionRoute(r ExplicitRoute) bool {
	return r.Name != ""
}

func isReservedAction(name string) bool {
	switch name {
	case model.List.Name, model.Get.Name, model.Create.Name, model.Update.Name,
		model.Delete.Name, model.Preview.Name, model.Pivot.Name:
		return true
	default:
		return false
	}
}

func actionClaimed(claimed map[actionIdentity]bool, moduleName string, md model.ModelDeclaration, action string) bool {
	return claimed[actionIdentity{md.QualifiedName(moduleName), action}]
}

// explicitActionIdentities lists the (model, action name) of every
// engine.DefineAction route the module has already registered into table, so an
// EnableOps or workflow-transition candidate for the same identity is
// recognized as overridden without comparing paths.
func explicitActionIdentities(table *RouteTable, moduleName string) map[actionIdentity]bool {
	claimed := make(map[actionIdentity]bool)
	for _, r := range table.All() {
		m := r.Entry.Manifest
		if r.Entry.ModuleName == moduleName && !m.EngineNative && m.Name != "" {
			claimed[actionIdentity{m.Model, m.Name}] = true
		}
	}
	return claimed
}
