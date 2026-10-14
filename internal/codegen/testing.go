package codegen

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"
	"strings"

	abiv1 "github.com/djangbahevans/goerp/contract/abi/v1"
	enginemodule "github.com/djangbahevans/goerp/internal/engine/module"
	"github.com/djangbahevans/goerp/internal/engine/registry"
	"github.com/djangbahevans/goerp/internal/engine/route"
	"github.com/djangbahevans/goerp/sdk/go/model"
)

type TestingParameter struct {
	Name     string `json:"name"`
	Source   string `json:"source"`
	WireName string `json:"wire_name,omitempty"`
}

type TestingFunction struct {
	Method     string             `json:"method"`
	Path       string             `json:"path"`
	Parameters []TestingParameter `json:"parameters"`
	Response   string             `json:"response"`
}

func (g *generator) testingFixture() ([]byte, error) {
	fixture := struct {
		Module    string                     `json:"module"`
		Schema    map[string]any             `json:"schema"`
		Functions map[string]TestingFunction `json:"functions"`
	}{
		Module:    g.in.Module,
		Schema:    g.in.TestingSchema,
		Functions: g.functions,
	}

	raw, err := json.Marshal(fixture, json.Deterministic(true), jsontext.Multiline(true), jsontext.WithIndent("  "))
	if err != nil {
		return nil, fmt.Errorf("encode testing fixture: %w", err)
	}
	return append(raw, '\n'), nil
}

func responseEncoding(list bool) string {
	if list {
		return "paged"
	}
	return "value"
}

func (g *generator) schemaRoute(modelName, name string) (method, path string) {
	modules, _ := g.in.TestingSchema["modules"].(map[string]any)
	mod, _ := modules[g.in.Module].(map[string]any)
	routes, _ := mod["routes"].([]any)
	for _, raw := range routes {
		r, _ := raw.(map[string]any)
		if r["model"] != modelName {
			continue
		}
		if r["name"] == name || (r["engine_native"] == true && r["crud_action"] == name) {
			method, _ = r["method"].(string)
			path, _ = r["path"].(string)
		}
	}
	return method, path
}

func (g *generator) recordCRUD(fn string, m Model, op string) {
	method, path := g.schemaRoute(m.Name, op)
	var params []TestingParameter
	switch op {
	case "list":
		params = append(params, TestingParameter{Name: "params", Source: "list_query"})
	case "get", "delete":
		params = append(params, TestingParameter{Name: "id", Source: "path", WireName: "id"})
	case "create":
		params = append(params, TestingParameter{Name: "body", Source: "body"})
	case "update":
		params = append(params,
			TestingParameter{Name: "id", Source: "path", WireName: "id"},
			TestingParameter{Name: "body", Source: "body"},
			TestingParameter{Name: "expectedEtag", Source: "etag"},
		)
	}
	response := responseEncoding(op == "list")
	if op == "delete" {
		response = "void"
	}
	g.functions[fn] = TestingFunction{
		Method:     method,
		Path:       path,
		Parameters: params,
		Response:   response,
	}
}

func (g *generator) recordAction(fn string, r Route) {
	method, path := g.schemaRoute(r.Model, r.Name)
	var params []TestingParameter
	if r.Scope != CollectionScope {
		params = append(params, TestingParameter{Name: "id", Source: "path", WireName: "id"})
	}
	if r.RequestType != nil {
		params = append(params, TestingParameter{Name: "body", Source: "body"})
	}
	g.functions[fn] = TestingFunction{
		Method:     strings.ToUpper(method),
		Path:       path,
		Parameters: params,
		Response:   responseEncoding(r.ResponseIsList),
	}
}

func (mf *localManifest) testingSchema(routes []abiv1.RouteDeclaration, sch model.Schema) (map[string]any, error) {
	loaded := &enginemodule.LoadedModule{
		Manifest:       mf.Full,
		ExplicitRoutes: routes,
		TypeDecls:      sch.Types,
	}
	for _, md := range sch.Models {
		if md != nil {
			loaded.ModelDecls = append(loaded.ModelDecls, *md)
		}
	}
	views, _, navigation, err := route.SynthesizeViews(mf.Name, mf.Full.Type, loaded.ModelDecls, mf.Full.Views, mf.Full.Navigation)
	if err != nil {
		return nil, fmt.Errorf("synthesize testing views: %w", err)
	}
	loaded.Manifest.Views = append(loaded.Manifest.Views, views...)
	loaded.Manifest.Navigation = navigation
	snapshot, err := (&registry.ModuleRegistry{}).Update(map[string]*enginemodule.LoadedModule{mf.Name: loaded})
	if err != nil {
		return nil, fmt.Errorf("build testing schema: %w", err)
	}
	schemaJSON, err := json.Marshal(snapshot.SchemaResponse())
	if err != nil {
		return nil, fmt.Errorf("encode testing schema: %w", err)
	}
	var schema map[string]any
	if err := json.Unmarshal(schemaJSON, &schema); err != nil {
		return nil, fmt.Errorf("decode testing schema: %w", err)
	}
	return schema, nil
}
