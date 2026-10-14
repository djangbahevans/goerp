package registry

import (
	"encoding/json/v2"
	"strings"
	"testing"

	"github.com/djangbahevans/goerp/internal/engine/manifest"
	"github.com/djangbahevans/goerp/internal/engine/module"
	"github.com/djangbahevans/goerp/internal/engine/route"
)

func TestSchemaResponseFormFieldPermission(t *testing.T) {
	const permission = "contacts:contact:financials_read"
	var view manifest.View
	if err := json.Unmarshal([]byte(`{
		"name":"contact_form","type":"form","resource":"contacts.contact",
		"sections":[{"fields":[{"field":"credit_limit","permission":"`+permission+`"},{"field":"name"}]}]
	}`), &view); err != nil {
		t.Fatal(err)
	}

	field := view.Sections[0].Fields[0]
	if field.Permission != permission {
		t.Fatalf("Permission = %q, want %q", field.Permission, permission)
	}
	if _, ok := field.Extra["permission"]; ok {
		t.Fatal("permission must decode as a typed field")
	}

	modules := map[string]*module.LoadedModule{
		"contacts": {
			Manifest: manifest.Manifest{
				Name:  "contacts",
				Views: []manifest.View{view},
			},
		},
	}
	routes := route.New()
	hash := computeSchemaHash(modules, routes)
	response := buildSchemaResponse(modules, routes, hash)
	encoded, err := json.Marshal(response)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(string(encoded), `"permission":`); got != 1 {
		t.Fatalf("permission key count = %d, want 1; schema = %s", got, encoded)
	}

	var served SchemaResponse
	if err := json.Unmarshal(encoded, &served); err != nil {
		t.Fatal(err)
	}
	fields := served.Modules["contacts"].Views[0].Sections[0].Fields
	if fields[0].Permission != permission || fields[1].Permission != "" {
		t.Fatalf("served fields = %+v, want gated credit_limit and unrestricted name", fields)
	}

	modules["contacts"].Manifest.Views[0].Sections[0].Fields[0].Permission = ""
	if computeSchemaHash(modules, routes) == hash {
		t.Fatal("schema hash must change when a form field permission changes")
	}
}
