package codegen

import (
	"cmp"
	"context"
	"encoding/json/v2"
	"fmt"
	"io"
	"maps"
	"net/http"
	"slices"
	"strings"

	abiv1 "github.com/djangbahevans/goerp/contract/abi/v1"
)

// The /_meta/schema shapes a --from-engine run reads
// (shell-architecture.md §9 MetaSchema) — only the members codegen uses.
type (
	metaSchema struct {
		Modules map[string]metaModule `json:"modules"`
	}
	metaModule struct {
		Routes    []metaRoute          `json:"routes"`
		Views     []metaView           `json:"views"`
		Models    map[string]metaModel `json:"models"`
		LoadOrder int                  `json:"load_order"`
	}
	metaRoute struct {
		Method         string        `json:"method"`
		Path           string        `json:"path"`
		Model          string        `json:"model"`
		CrudAction     string        `json:"crud_action"`
		Name           string        `json:"name"`
		ResponseIsList bool          `json:"response_is_list"`
		Streaming      bool          `json:"streaming"`
		Websocket      bool          `json:"websocket"`
		EngineNative   bool          `json:"engine_native"`
		RequestType    *metaTypeDesc `json:"request_type"`
		ResponseType   *metaTypeDesc `json:"response_type"`
	}
	metaTypeDesc struct {
		Kind     string          `json:"kind"`
		Name     string          `json:"name"`
		Elem     *metaTypeDesc   `json:"elem"`
		Fields   []metaFieldDesc `json:"fields"`
		Nullable bool            `json:"nullable"`
	}
	metaFieldDesc struct {
		Name     string       `json:"name"`
		Type     metaTypeDesc `json:"type"`
		Optional bool         `json:"optional"`
	}
	metaModel struct {
		LabelPlural string      `json:"label_plural"`
		Fields      []metaField `json:"fields"`
		EnabledOps  []string    `json:"enabled_ops"`
	}
	metaField struct {
		Name            string   `json:"name"`
		Type            string   `json:"type"`
		Required        bool     `json:"required"`
		SelectionValues []string `json:"selection_values"`
		Readonly        bool     `json:"readonly"`
		PrimaryKey      bool     `json:"primary_key"`
		HasDefault      bool     `json:"has_default"`
	}
	metaView struct {
		Name          string       `json:"name"`
		Type          string       `json:"type"`
		Resource      string       `json:"resource"`
		FetchRoute    string       `json:"fetch_route"`
		CreateRoute   string       `json:"create_route"`
		UpdateRoute   string       `json:"update_route"`
		DeleteRoute   string       `json:"delete_route"`
		Actions       []ViewAction `json:"actions"`
		BulkActions   []ViewAction `json:"bulk_actions"`
		HeaderActions []ViewAction `json:"header_actions"`
	}
)

// FetchSchema reads GET {baseURL}/_meta/schema from the main application
// server, authenticated with a per-tenant API key. baseURL must be the
// tenant's own host, since the engine resolves the tenant from it.
func FetchSchema(ctx context.Context, client *http.Client, baseURL, token string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimSuffix(baseURL, "/")+"/_meta/schema", nil)
	if err != nil {
		return nil, fmt.Errorf("build /_meta/schema request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch /_meta/schema: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read /_meta/schema response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		snippet := string(body)
		if len(snippet) > 512 {
			snippet = snippet[:512]
		}
		return nil, fmt.Errorf("fetch /_meta/schema: %s: %s", resp.Status, strings.TrimSpace(snippet))
	}
	return body, nil
}

// InputFromSchema builds module's Input from a /_meta/schema response.
// Each of module's models also gets the fields any other module adds to it
// under the same qualified name (model.Extend), after its own and in load
// order. Every module in the response joins the view-validation catalog.
func InputFromSchema(raw []byte, module string) (*Input, error) {
	var schema metaSchema
	if err := json.Unmarshal(raw, &schema); err != nil {
		return nil, fmt.Errorf("decode /_meta/schema response: %w", err)
	}
	target, ok := schema.Modules[module]
	if !ok {
		return nil, fmt.Errorf("module %s is not loaded on the engine", module)
	}

	in := &Input{Module: module, PathPrefix: "/" + module, Catalog: map[string]*CatalogModule{}}
	connectorPrefix := "/connectors/" + module
	for _, r := range target.Routes {
		if r.Path == connectorPrefix || strings.HasPrefix(r.Path, connectorPrefix+"/") {
			in.PathPrefix = connectorPrefix
			break
		}
	}

	// Modules other than the target, in load order, for model.Extend
	// fields; module name breaks a load-order tie.
	others := make([]string, 0, len(schema.Modules))
	for name := range schema.Modules {
		if name != module {
			others = append(others, name)
		}
	}
	slices.SortFunc(others, func(a, b string) int {
		return cmp.Or(cmp.Compare(schema.Modules[a].LoadOrder, schema.Modules[b].LoadOrder), strings.Compare(a, b))
	})

	for _, name := range slices.Sorted(maps.Keys(target.Models)) {
		m := modelFromMeta(name, target.Models[name])
		for _, other := range others {
			if ext, ok := schema.Modules[other].Models[name]; ok {
				m.Fields = mergeFields(m.Fields, modelFromMeta(name, ext).Fields)
			}
		}
		in.Models = append(in.Models, m)
	}

	for _, r := range target.Routes {
		route, ok := routeFromMeta(r, in.PathPrefix)
		if ok {
			in.Routes = append(in.Routes, route)
		}
	}

	for _, v := range target.Views {
		in.Views = append(in.Views, View{
			Name:        v.Name,
			Type:        v.Type,
			Resource:    v.Resource,
			FetchRoute:  v.FetchRoute,
			CreateRoute: v.CreateRoute,
			UpdateRoute: v.UpdateRoute,
			DeleteRoute: v.DeleteRoute,
			Actions:     slices.Concat(v.Actions, v.BulkActions, v.HeaderActions),
		})
	}

	for name, mod := range schema.Modules {
		var models []Model
		var routes []Route
		for qualified, md := range mod.Models {
			models = append(models, modelFromMeta(qualified, md))
		}
		for _, r := range mod.Routes {
			routes = append(routes, Route{Model: r.Model, Name: r.Name, CRUDAction: r.CrudAction})
		}
		in.Catalog[name] = catalogFor(models, routes)
	}

	return in, nil
}

func modelFromMeta(qualified string, md metaModel) Model {
	m := Model{Name: qualified, LabelPlural: md.LabelPlural, Ops: md.EnabledOps}
	for _, f := range md.Fields {
		m.Fields = append(m.Fields, Field{
			Name:       f.Name,
			Kind:       f.Type,
			Required:   f.Required,
			PrimaryKey: f.PrimaryKey,
			HasDefault: f.HasDefault,
			Readonly:   f.Readonly,
			Values:     f.SelectionValues,
		})
	}
	return m
}

// mergeFields appends each of ext's fields whose name base doesn't
// already have.
func mergeFields(base, ext []Field) []Field {
	for _, f := range ext {
		if !slices.ContainsFunc(base, func(b Field) bool { return b.Name == f.Name }) {
			base = append(base, f)
		}
	}
	return base
}

// routeFromMeta converts one /_meta/schema route of the target module. It
// reports false for an EnableOps CRUD route, whose functions come from the
// model's ops instead, as in a local run, where get_routes never lists it.
// Its path loses the module prefix, and an engine.DefineAction's scope is read
// back from whether its path addresses one record.
func routeFromMeta(r metaRoute, prefix string) (Route, bool) {
	if r.EngineNative && r.Name == "" {
		return Route{}, false
	}
	route := Route{
		Method:         r.Method,
		Model:          r.Model,
		Name:           r.Name,
		CRUDAction:     r.CrudAction,
		ResponseIsList: r.ResponseIsList,
		Streaming:      r.Streaming,
		Websocket:      r.Websocket,
		RequestType:    typeDescFromMeta(r.RequestType),
		ResponseType:   typeDescFromMeta(r.ResponseType),
	}
	if r.Name != "" {
		route.Scope = CollectionScope
		if strings.Contains(r.Path, "{") {
			route.Scope = RecordScope
		}
		return route, true
	}
	route.Path = strings.TrimPrefix(r.Path, prefix)
	if route.Path == "" {
		route.Path = "/"
	}
	return route, true
}

func typeDescFromMeta(d *metaTypeDesc) *abiv1.TypeDesc {
	if d == nil {
		return nil
	}
	out := &abiv1.TypeDesc{
		Kind:     abiv1.TypeKind(d.Kind),
		Name:     d.Name,
		Elem:     typeDescFromMeta(d.Elem),
		Nullable: d.Nullable,
	}
	for _, f := range d.Fields {
		out.Fields = append(out.Fields, abiv1.FieldDesc{Name: f.Name, Type: *typeDescFromMeta(&f.Type), Optional: f.Optional})
	}
	return out
}
