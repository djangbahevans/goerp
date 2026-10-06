// Package codegen implements goerp codegen: it generates a module's
// TypeScript API client (typescript-sdk-reference.md §4) from its routes and
// model declarations, read either from a local build (LoadLocal) or from a
// running engine's /_meta/schema (FetchSchema, InputFromSchema).
package codegen

import (
	abiv1 "github.com/djangbahevans/goerp/contract/abi/v1"
)

// Input is everything Generate needs about one module, independent of
// whether it came from a local build or a running engine.
type Input struct {
	// Module is the module's name, e.g. "contacts".
	Module string
	// PathPrefix is the prefix the engine puts in front of the module's
	// declared route paths: "/{module}", or "/connectors/{module}" for a
	// connector-type module.
	PathPrefix string

	Models []Model
	Routes []Route
	Views  []View
	// ActionRefs are the useAction/callAction names the module's frontend
	// source passes (ScanActionRefs), checked like a view's "route" action.
	ActionRefs []ActionRef

	// Catalog describes every module whose resources and actions a view
	// may reference, including this one — only this module for a local
	// build, every loaded module for --from-engine. A view reference into
	// a module that isn't in Catalog is not validated.
	Catalog map[string]*CatalogModule
}

// Model is one model the module declares, with any model.Extend fields
// other modules add to it already merged in (--from-engine only).
type Model struct {
	// Name is the model's qualified name, "{module}.{resource}".
	Name        string
	LabelPlural string
	Fields      []Field
	// Ops is the model's EnableOps allowlist, by op name.
	Ops []string
}

// Field is one model field, carrying only what the generated types depend
// on.
type Field struct {
	Name string
	// Kind is the field constructor's name, lowercased — model.FieldKind's
	// String() form ("char", "many2one", ...).
	Kind       string
	Required   bool
	PrimaryKey bool
	// HasDefault reports a database default, which makes a required field
	// optional on create.
	HasDefault bool
	// Readonly reports a field the ORM rejects in a create or update body:
	// declared Readonly or computed.
	Readonly bool
	// Values are a selection field's values or an enum field's type
	// values.
	Values []string
}

// Route is one route the module registers itself — an engine.DefineAction or a
// raw engine.GET/POST/... route. EnableOps CRUD routes are not Routes: they
// come from Model.Ops.
type Route struct {
	Method string
	// Path is the module-relative declared path of a raw route, e.g.
	// "/by-email/{email}"; empty for an engine.DefineAction route.
	Path string

	Model string
	// Name is an engine.DefineAction route's action name; empty for a raw route.
	Name       string
	CRUDAction string
	// Scope is an engine.DefineAction route's scope, "record" or "collection".
	Scope string

	ResponseIsList bool
	// Streaming and Websocket mark engine.SSE and engine.WS routes, which
	// get no generated function.
	Streaming bool
	Websocket bool

	RequestType  *abiv1.TypeDesc
	ResponseType *abiv1.TypeDesc
}

// View is the part of a view declaration build-time validation checks.
type View struct {
	Name     string
	Type     string
	Resource string

	// FetchRoute, CreateRoute, UpdateRoute and DeleteRoute replace the
	// resource's own op for that operation when set.
	FetchRoute  string
	CreateRoute string
	UpdateRoute string
	DeleteRoute string

	Actions []ViewAction
}

// ViewAction is one entry of a view's actions, bulk_actions or
// header_actions.
type ViewAction struct {
	Label string `json:"label"`
	Type  string `json:"type"`
	Route string `json:"route"`
}

// CatalogModule is what view validation knows about one module.
type CatalogModule struct {
	// Ops maps each of the module's qualified model names to the ops it
	// supports: its EnableOps allowlist plus any reserved-name
	// engine.DefineAction override.
	Ops map[string]map[string]bool
	// Actions is every action name a "route" view action may reference:
	// engine.DefineAction names and workflow transition action names.
	Actions map[string]bool
}

// RecordScope and CollectionScope are Route.Scope's values.
const (
	RecordScope     = "record"
	CollectionScope = "collection"
)
