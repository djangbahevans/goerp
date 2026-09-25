package registry

import (
	abiv1 "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/djangbahevans/goerp/internal/engine/manifest"
	"github.com/djangbahevans/goerp/internal/engine/module"
	"github.com/djangbahevans/goerp/internal/engine/route"
	"github.com/djangbahevans/goerp/sdk/go/model"
)

// schemaEngineVersion is the running binary's own build version, reported
// as SchemaResponse.EngineVersion (goerp#573) — "dev" until a real
// build-time version is injected, matching /_health's identical
// placeholder rather than inventing a second, possibly-inconsistent
// convention.
const schemaEngineVersion = "dev"

// SchemaResponse is GET /_meta/schema's response shape —
// shell-architecture.md §9 "/_meta/schema response shape" (MetaSchema) is
// the canonical type reference; view-system.md §2 has a shorter
// illustrative example of the same endpoint. Built once per published
// RegistrySnapshot (buildSchemaResponse, called from UpdateWithLocked)
// rather than per request (goerp#591) — it's a pure function of the
// snapshot's modules and route table, invariant until the next reload.
type SchemaResponse struct {
	Modules       map[string]*SchemaModule `json:"modules"`
	EngineVersion string                   `json:"engine_version"`
	SchemaHash    string                   `json:"schema_hash"`
}

type SchemaModule struct {
	Name        string              `json:"name"`
	Version     string              `json:"version"`
	DisplayName string              `json:"display_name"`
	Routes      []SchemaRoute       `json:"routes"`
	Views       []manifest.View     `json:"views"`
	Navigation  []manifest.NavGroup `json:"navigation"`
	// ViewExtensions and ViewExtensionDefinitions are this module's
	// declared view_extensions/view_extension_definitions (manifest-spec.md
	// §11), serialized as [] rather than omitted when the module declares
	// none — the shell indexes every module's slice unconditionally when
	// building its view-extension registry (view-system.md §10).
	ViewExtensions           []manifest.ViewExtensionRef `json:"view_extensions"`
	ViewExtensionDefinitions []manifest.ViewExtensionDef `json:"view_extension_definitions"`
	// LoadOrder is module.LoadedModule.LoadOrder — this module's index in
	// the engine's dependency-ordered load sequence, so the shell can apply
	// view extensions dependencies-first without re-deriving the graph.
	LoadOrder    int                    `json:"load_order"`
	Models       map[string]SchemaModel `json:"models"`
	Permissions  []SchemaPermission     `json:"permissions"`
	Frontend     *SchemaFrontend        `json:"frontend"`
	PublicConfig map[string]any         `json:"public_config"`
}

// SchemaRoute is shell-architecture.md §9's RouteSchema — a subset of
// RouteManifest, only the fields the shell needs. Path is always the
// engine-expanded path (RouteEntry.PathTemplate) — not the module-relative
// declared path, which isn't part of this contract.
type SchemaRoute struct {
	Method         string   `json:"method"`
	Path           string   `json:"path"`
	Permissions    []string `json:"permissions"`
	Model          string   `json:"model,omitempty"`
	CrudAction     string   `json:"crud_action,omitempty"`
	Name           string   `json:"name,omitempty"`
	ResponseIsList bool     `json:"response_is_list"`
	View           string   `json:"view,omitempty"`
	// engine.Body/engine.Returns declarations, read by goerp codegen.
	RequestType  *SchemaTypeDesc `json:"request_type,omitempty"`
	ResponseType *SchemaTypeDesc `json:"response_type,omitempty"`
}

// SchemaTypeDesc is go-sdk-reference.md §2a's TypeDesc in JSON form.
type SchemaTypeDesc struct {
	Kind     string            `json:"kind"`
	Name     string            `json:"name,omitempty"`
	Elem     *SchemaTypeDesc   `json:"elem,omitempty"`
	Fields   []SchemaFieldDesc `json:"fields,omitempty"`
	Nullable bool              `json:"nullable,omitzero"`
}

// SchemaFieldDesc is one field of an object SchemaTypeDesc.
type SchemaFieldDesc struct {
	Name     string         `json:"name"`
	Type     SchemaTypeDesc `json:"type"`
	Optional bool           `json:"optional,omitzero"`
}

func schemaTypeDescFrom(d *abiv1.TypeDesc) *SchemaTypeDesc {
	if d == nil {
		return nil
	}
	out := &SchemaTypeDesc{
		Kind:     string(d.Kind),
		Name:     d.Name,
		Elem:     schemaTypeDescFrom(d.Elem),
		Nullable: d.Nullable,
	}
	if len(d.Fields) > 0 {
		out.Fields = make([]SchemaFieldDesc, len(d.Fields))
		for i, f := range d.Fields {
			out.Fields[i] = SchemaFieldDesc{Name: f.Name, Type: *schemaTypeDescFrom(&f.Type), Optional: f.Optional}
		}
	}
	return out
}

// SchemaModel is shell-architecture.md §9's ModelDef.
type SchemaModel struct {
	Name        string        `json:"name"`
	Label       string        `json:"label"`
	LabelPlural string        `json:"label_plural"`
	Fields      []SchemaField `json:"fields"`
	EnabledOps  []string      `json:"enabled_ops"`
	Shareable   bool          `json:"shareable"`
	// SharePermissions lists the access levels .Shareable(perms...) accepts,
	// in declared order; omitted for a model that isn't shareable.
	SharePermissions []string `json:"share_permissions,omitempty"`
}

// SchemaField is shell-architecture.md §9's FieldDef.
type SchemaField struct {
	Name         string          `json:"name"`
	Type         string          `json:"type"`
	Required     bool            `json:"required,omitzero"`
	RelatedModel string          `json:"related_model,omitempty"`
	InverseField string          `json:"inverse_field,omitempty"`
	IsPrimary    bool            `json:"is_primary,omitzero"`
	Workflow     *SchemaWorkflow `json:"workflow,omitempty"`
}

// SchemaWorkflow exposes a .Workflow()-declared Selection field's
// state/transition graph (go-sdk-reference.md "Declarative workflow
// transitions") — a form view's "workflow_actions": true reads this to
// auto-render transition buttons (view-system.md's "Auto-rendered
// transition buttons"), deriving each button's permission and
// route (POST {plural}/{id}/{action_name}) from it. States is the
// field's own SelectionValues — there's no separate state vocabulary to
// keep in sync with the field it governs.
type SchemaWorkflow struct {
	States      []string           `json:"states"`
	Transitions []SchemaTransition `json:"transitions"`
}

type SchemaTransition struct {
	From       string `json:"from"`
	To         string `json:"to"`
	ActionName string `json:"action_name"`
	Permission string `json:"permission,omitempty"`
	// Condition is the raw domain-expression string, passed through
	// unevaluated — the shell's own domain-expression interpreter
	// (goerp#829) evaluates it client-side to decide whether to show the
	// button; the engine doesn't evaluate it server-side yet either
	// (go-sdk-reference.md's ConditionExpr doc comment).
	Condition string `json:"condition,omitempty"`
}

// SchemaPermission is shell-architecture.md §9's PermissionDeclaration —
// a direct reflection of manifest.Permission, whose JSON tags already
// match the documented wire shape.
type SchemaPermission = manifest.Permission

// SchemaFrontend is shell-architecture.md §9's ModuleSchema.frontend — nil
// for a module whose manifest declares no frontend bundle (or bundle:
// false); populated with the currently-loaded version's URL/digest
// (goerp#588) otherwise.
type SchemaFrontend struct {
	BundleURL    string `json:"bundle_url"`
	BundleSHA256 string `json:"bundle_sha256"`
}

// buildSchemaResponse builds GET /_meta/schema's full response for modules
// and routeTable — called once per published snapshot (UpdateWithLocked),
// alongside computeSchemaHash's own walk of the same data, rather than
// once per request (goerp#591). schemaHash is the value UpdateWithLocked
// already computed via computeSchemaHash for this same snapshot, reused
// here rather than recomputed.
func buildSchemaResponse(modules map[string]*module.LoadedModule, routeTable *route.RouteTable, schemaHash string) *SchemaResponse {
	schemaModules := map[string]*SchemaModule{}
	for name, m := range modules {
		if m.Status == module.StatusFailed {
			continue
		}

		models := map[string]SchemaModel{}
		for _, md := range m.ModelDecls {
			models[md.QualifiedName(name)] = schemaModelFrom(md)
		}

		publicConfig := map[string]any{}
		for _, c := range m.Manifest.ConfigSchema {
			if c.Public {
				publicConfig[c.Key] = c.Default
			}
		}

		viewExtensions := m.Manifest.ViewExtensions
		if viewExtensions == nil {
			viewExtensions = []manifest.ViewExtensionRef{}
		}
		viewExtensionDefs := m.Manifest.ViewExtensionDefinitions
		if viewExtensionDefs == nil {
			viewExtensionDefs = []manifest.ViewExtensionDef{}
		}

		schemaModules[name] = &SchemaModule{
			Name:                     name,
			Version:                  m.Manifest.Version,
			DisplayName:              m.Manifest.DisplayName,
			Routes:                   []SchemaRoute{},
			Views:                    m.Manifest.Views,
			Navigation:               m.Manifest.Navigation,
			ViewExtensions:           viewExtensions,
			ViewExtensionDefinitions: viewExtensionDefs,
			LoadOrder:                m.LoadOrder,
			Models:                   models,
			Permissions:              m.Manifest.Permissions,
			Frontend:                 schemaFrontendFor(name, &m.Manifest),
			PublicConfig:             publicConfig,
		}
	}

	for _, rt := range routeTable.All() {
		mod, ok := schemaModules[rt.Entry.ModuleName]
		if !ok {
			// Either an engine built-in (ModuleName == "") or, in
			// principle, a module the route table disagrees with modules
			// about — unreachable in practice since both are read from the
			// same snapshot inputs, but not assumed.
			continue
		}

		mf := rt.Entry.Manifest
		mod.Routes = append(mod.Routes, SchemaRoute{
			Method:         rt.Method,
			Path:           rt.Entry.PathTemplate,
			Permissions:    mf.Permissions,
			Model:          mf.Model,
			CrudAction:     mf.CrudAction,
			Name:           mf.Name,
			ResponseIsList: mf.ResponseIsList,
			View:           schemaViewFor(mod.Views, mf.Model, mf.CrudAction),
			RequestType:    schemaTypeDescFrom(mf.RequestType),
			ResponseType:   schemaTypeDescFrom(mf.ResponseType),
		})
	}

	return &SchemaResponse{
		Modules:       schemaModules,
		EngineVersion: schemaEngineVersion,
		SchemaHash:    schemaHash,
	}
}

// schemaModelFrom builds md's ModelDef entry.
func schemaModelFrom(md model.ModelDeclaration) SchemaModel {
	fields := make([]SchemaField, 0, len(md.Fields))
	for _, f := range md.Fields {
		fields = append(fields, SchemaField{
			Name:         f.Name,
			Type:         f.Def.Kind.String(),
			Required:     f.Def.IsRequired,
			RelatedModel: f.Def.RelatedModel,
			InverseField: f.Def.InverseField,
			IsPrimary:    f.Def.IsPrimary,
			Workflow:     schemaWorkflowFrom(f.Def),
		})
	}
	ops := make([]string, 0, len(md.EnabledOps))
	for _, op := range md.EnabledOps {
		ops = append(ops, op.Name)
	}
	var sharePermissions []string
	if md.Shareable {
		for _, p := range md.SharePerms {
			sharePermissions = append(sharePermissions, string(p))
		}
	}
	return SchemaModel{
		Name:             md.ResourceName(),
		Label:            md.Label,
		LabelPlural:      md.LabelPlural,
		Fields:           fields,
		EnabledOps:       ops,
		Shareable:        md.Shareable,
		SharePermissions: sharePermissions,
	}
}

// schemaWorkflowFrom builds def's workflow schema entry, or nil if def has
// no .Workflow() declaration (the common case — most fields, and even
// most Selection fields, aren't a workflow gate).
func schemaWorkflowFrom(def model.FieldDef) *SchemaWorkflow {
	if len(def.WorkflowTransitions) == 0 {
		return nil
	}

	transitions := make([]SchemaTransition, 0, len(def.WorkflowTransitions))
	for _, t := range def.WorkflowTransitions {
		transitions = append(transitions, SchemaTransition{
			From:       t.From,
			To:         t.To,
			ActionName: t.ActionName,
			Permission: t.Permission,
			Condition:  t.ConditionExpr,
		})
	}

	return &SchemaWorkflow{
		States:      def.SelectionValues,
		Transitions: transitions,
	}
}

// schemaViewFor reports which of views (if any) route serves —
// RouteSchema.view. A "list"-shaped view (list, kanban, calendar, pivot,
// or timeline — all alternate visualizations of the same list dataset per
// view-system.md's overview) claims that resource's "list" CrudAction; a
// "form" view claims "get"/"create"/"update". This doesn't yet honor a
// view's own FetchRoute/CreateRoute/UpdateRoute/DeleteRoute override
// fields — refine once a view actually uses one.
// schemaFrontendFor builds moduleName's SchemaFrontend entry, or nil when
// mf declares no frontend bundle. moduleName has already loaded
// successfully by the time buildSchemaResponse reaches this (a
// StatusFailed module is skipped before this call), so mf.Frontend's own
// BundleSHA256 has already passed loader.LoadModule's verifyBundle check —
// BundleFilename erroring here would mean that check somehow didn't run,
// treated the same as "no bundle" rather than panicking a live /_meta/schema
// request over it.
func schemaFrontendFor(moduleName string, mf *manifest.Manifest) *SchemaFrontend {
	filename, err := module.BundleFilename(mf)
	if err != nil || filename == "" {
		return nil
	}

	return &SchemaFrontend{
		BundleURL:    "/modules/" + moduleName + "/frontend/" + filename,
		BundleSHA256: mf.Frontend.BundleSHA256,
	}
}

func schemaViewFor(views []manifest.View, modelName, crudAction string) string {
	for _, v := range views {
		if v.Resource != modelName {
			continue
		}
		switch {
		case v.Type == "form" && (crudAction == "get" || crudAction == "create" || crudAction == "update"):
			return v.Name
		case listShapedViewTypes[v.Type] && crudAction == "list":
			return v.Name
		}
	}
	return ""
}

// listShapedViewTypes are every view-system.md type that visualizes a
// list dataset (as opposed to a single record, "form") — all claim a
// resource's "list" CrudAction route in schemaViewFor. Kanban/calendar/
// pivot/timeline aren't built yet (backlog #26-#28), but their names are
// fixed by view-system.md's own type vocabulary, so this stays exhaustive
// rather than falling through by default for anything non-"form".
var listShapedViewTypes = map[string]bool{
	"list":     true,
	"kanban":   true,
	"calendar": true,
	"pivot":    true,
	"timeline": true,
}
