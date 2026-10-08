package registry

import (
	abiv1 "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/djangbahevans/goerp/internal/engine/enginenotif"
	"github.com/djangbahevans/goerp/internal/engine/manifest"
	"github.com/djangbahevans/goerp/internal/engine/module"
	"github.com/djangbahevans/goerp/internal/engine/route"
	"github.com/djangbahevans/goerp/sdk/go/model"
)

// schemaEngineVersion matches the health endpoint's development build version.
const schemaEngineVersion = "dev"

// SchemaResponse carries GET /_meta/schema's API declarations. It is built once per
// registry snapshot and remains invariant until the next publish.
type SchemaResponse struct {
	Modules map[string]*SchemaModule `json:"modules"`
	// EngineNotificationTypes are the engine's own notification types
	// (notification-system.md §6 "Engine-declared notification types"),
	// listed apart from Modules because "engine" is no module's name.
	EngineNotificationTypes []SchemaNotificationType `json:"engine_notification_types"`
	EngineVersion           string                   `json:"engine_version"`
	SchemaHash              string                   `json:"schema_hash"`
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
	// NotificationTypes is [] rather than omitted when the module declares
	// none; the shell's notification preferences page lists every module's.
	NotificationTypes []SchemaNotificationType `json:"notification_types"`
}

// SchemaNotificationType exposes manifest notification settings needed by the user's
// preferences page.
type SchemaNotificationType struct {
	Name              string   `json:"name"`
	Label             string   `json:"label"`
	Description       string   `json:"description,omitempty"`
	AvailableChannels []string `json:"available_channels"`
}

func schemaNotificationTypesFrom(types []manifest.NotificationType) []SchemaNotificationType {
	out := make([]SchemaNotificationType, 0, len(types))
	for _, nt := range types {
		out = append(out, SchemaNotificationType{
			Name:              nt.Name,
			Label:             nt.Label,
			Description:       nt.Description,
			AvailableChannels: nt.AvailableChannels,
		})
	}
	return out
}

// SchemaRoute is shell-architecture.md §9's RouteSchema — a subset of
// RouteManifest, only the fields the shell needs. Path is always the
// engine-expanded path (RouteEntry.PathTemplate) — not the module-relative
// declared path, which isn't part of this contract. Raw engine.Model routes
// retain their field-security binding; CRUD consumers require EngineNative
// or a Name matching the reserved CrudAction.
type SchemaRoute struct {
	Method         string   `json:"method"`
	Path           string   `json:"path"`
	Permissions    []string `json:"permissions"`
	Model          string   `json:"model,omitempty"`
	CrudAction     string   `json:"crud_action,omitempty"`
	Name           string   `json:"name,omitempty"`
	ResponseIsList bool     `json:"response_is_list"`
	View           string   `json:"view,omitempty"`
	// Streaming and Websocket mark engine.SSE and engine.WS routes, which
	// goerp codegen generates no function for.
	Streaming bool `json:"streaming,omitzero"`
	Websocket bool `json:"websocket,omitzero"`
	// EngineNative marks a route the engine serves with no module code —
	// an EnableOps CRUD route or a workflow transition — which goerp
	// codegen covers from the model instead of the route.
	EngineNative bool `json:"engine_native,omitzero"`
	// engine.Body/engine.Returns declarations, read by goerp codegen.
	RequestType  *SchemaTypeDesc `json:"request_type,omitempty"`
	ResponseType *SchemaTypeDesc `json:"response_type,omitempty"`
}

// SchemaTypeDesc describes JSON request/response shapes in the schema response.
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

// SchemaModel describes a module's model declaration in the schema response.
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

// SchemaField describes a declared model field in the schema response.
type SchemaField struct {
	Name         string          `json:"name"`
	Type         string          `json:"type"`
	Required     bool            `json:"required,omitzero"`
	RelatedModel string          `json:"related_model,omitempty"`
	InverseField string          `json:"inverse_field,omitempty"`
	IsPrimary    bool            `json:"is_primary,omitzero"`
	Workflow     *SchemaWorkflow `json:"workflow,omitempty"`
	// SelectionValues are a selection field's values, or an enum field's
	// type values.
	SelectionValues []string `json:"selection_values,omitempty"`
	// Readonly marks a field the ORM rejects in a create or update body:
	// declared Readonly, or computed.
	Readonly   bool `json:"readonly,omitzero"`
	PrimaryKey bool `json:"primary_key,omitzero"`
	// HasDefault marks a field with a database default, which a create
	// may omit even when it's required.
	HasDefault bool `json:"has_default,omitzero"`
	// Default is the value that default stores when it is a literal the shell
	// can show on a create form; absent for a computed default such as now().
	Default any `json:"default,omitempty"`
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
	// Condition is passed through as a raw domain expression for client-side button
	// visibility; server-side transitions do not enforce it.
	Condition string `json:"condition,omitempty"`
}

// SchemaPermission exposes the manifest permission declaration in the schema response.
type SchemaPermission = manifest.Permission

// SchemaFrontend supplies the loaded bundle's URL and digest; it is nil when no frontend
// bundle is declared.
type SchemaFrontend struct {
	BundleURL    string `json:"bundle_url"`
	BundleSHA256 string `json:"bundle_sha256"`
}

// buildSchemaResponse reuses the hash computed for the same registry snapshot.
func buildSchemaResponse(modules map[string]*module.LoadedModule, routeTable *route.RouteTable, schemaHash string) *SchemaResponse {
	schemaModules := map[string]*SchemaModule{}
	for name, m := range modules {
		if m.Status == module.StatusFailed {
			continue
		}

		models := map[string]SchemaModel{}
		for _, md := range m.ModelDecls {
			models[md.QualifiedName(name)] = schemaModelFrom(md, m.TypeDecls)
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
			NotificationTypes:        schemaNotificationTypesFrom(m.Manifest.NotificationTypes),
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
			Streaming:      mf.Streaming,
			Websocket:      mf.Websocket,
			EngineNative:   mf.EngineNative,
			RequestType:    schemaTypeDescFrom(mf.RequestType),
			ResponseType:   schemaTypeDescFrom(mf.ResponseType),
		})
	}

	return &SchemaResponse{
		Modules:                 schemaModules,
		EngineNotificationTypes: schemaNotificationTypesFrom(enginenotif.Types),
		EngineVersion:           schemaEngineVersion,
		SchemaHash:              schemaHash,
	}
}

// schemaModelFrom builds md's ModelDef entry, resolving an enum field's
// values from its module's declared types.
func schemaModelFrom(md model.ModelDeclaration, types []model.TypeDeclaration) SchemaModel {
	fields := make([]SchemaField, 0, len(md.Fields))
	for _, f := range md.Fields {
		values := f.Def.SelectionValues
		if f.Def.Kind == model.KindEnum {
			values = nil
			for _, t := range types {
				if t.Name == f.Def.EnumType {
					values = t.Values
					break
				}
			}
		}
		var defaultValue any
		if f.Def.DefaultExpr != nil {
			defaultValue, _ = staticDefault(f.Def.Kind, *f.Def.DefaultExpr)
		}
		fields = append(fields, SchemaField{
			Name:            f.Name,
			Type:            f.Def.Kind.String(),
			Required:        f.Def.IsRequired,
			RelatedModel:    f.Def.RelatedModel,
			InverseField:    f.Def.InverseField,
			IsPrimary:       f.Def.IsPrimary,
			Workflow:        schemaWorkflowFrom(f.Def),
			SelectionValues: values,
			Readonly:        f.Def.IsReadonly || f.Def.IsComputed,
			PrimaryKey:      f.Def.IsPrimaryKey,
			HasDefault:      f.Def.DefaultExpr != nil,
			Default:         defaultValue,
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

// schemaFrontendFor returns verified bundle metadata, or nil when the manifest declares no
// bundle or its filename cannot be derived.
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

// List-shaped views select the resource's list CRUD route; form selects a single-record
// route.
var listShapedViewTypes = map[string]bool{
	"list":     true,
	"kanban":   true,
	"calendar": true,
	"pivot":    true,
	"timeline": true,
}
