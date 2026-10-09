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
		Routes []metaRoute          `json:"routes"`
		Views  []metaView           `json:"views"`
		Models map[string]metaModel `json:"models"`
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

// InputFromSchema reads effective model fields and the global view-validation catalog.
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

	for _, name := range slices.Sorted(maps.Keys(target.Models)) {
		in.Models = append(in.Models, modelFromMeta(name, target.Models[name]))
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
	route.Path = cmp.Or(strings.TrimPrefix(r.Path, prefix), "/")
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
