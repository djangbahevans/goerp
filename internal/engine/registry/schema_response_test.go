package registry

import (
	"encoding/json/v2"
	"slices"
	"strings"
	"testing"

	"github.com/djangbahevans/goerp/internal/engine/manifest"
	"github.com/djangbahevans/goerp/internal/engine/module"
	"github.com/djangbahevans/goerp/sdk/go/engine"
	"github.com/djangbahevans/goerp/sdk/go/model"
)

func TestSchemaModelFrom_SharePermissions(t *testing.T) {
	tests := []struct {
		name string
		md   model.ModelDeclaration
		want []string
	}{
		{"both levels, in declared order", model.ModelDeclaration{Shareable: true, SharePerms: []model.SharePermission{model.WriteShare, model.ReadShare}}, []string{"write", "read"}},
		{"one level", model.ModelDeclaration{Shareable: true, SharePerms: []model.SharePermission{model.ReadShare}}, []string{"read"}},
		{"shareable with no levels", model.ModelDeclaration{Shareable: true}, nil},
		{"not shareable", model.ModelDeclaration{SharePerms: []model.SharePermission{model.ReadShare}}, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := schemaModelFrom(tt.md)
			if !slices.Equal(got.SharePermissions, tt.want) {
				t.Errorf("SharePermissions = %v, want %v", got.SharePermissions, tt.want)
			}
			out, err := json.Marshal(got)
			if err != nil {
				t.Fatalf("Marshal() error: %v", err)
			}
			hasKey := strings.Contains(string(out), `"share_permissions"`)
			if hasKey != (len(tt.want) > 0) {
				t.Errorf("share_permissions key present = %v for %v, want %v; body: %s", hasKey, tt.want, len(tt.want) > 0, out)
			}
		})
	}
}

func TestSchemaWorkflowFrom_NoWorkflowIsNil(t *testing.T) {
	if got := schemaWorkflowFrom(model.Char()); got != nil {
		t.Errorf("schemaWorkflowFrom(Char()) = %+v, want nil", got)
	}
	if got := schemaWorkflowFrom(model.Selection("a", "b")); got != nil {
		t.Errorf("schemaWorkflowFrom(no .Workflow()) = %+v, want nil", got)
	}
}

func TestSchemaViewFor(t *testing.T) {
	views := []manifest.View{
		{Name: "widgets.kanban", Type: "kanban", Resource: "widgets.widget"},
		{Name: "widgets.list", Type: "list", Resource: "widgets.widget"},
		{Name: "widgets.form", Type: "form", Resource: "widgets.widget"},
		{Name: "other.list", Type: "list", Resource: "other.thing"},
	}

	cases := []struct {
		crudAction string
		want       string
	}{
		{"list", "widgets.kanban"}, // first list-shaped match wins by declaration order
		{"get", "widgets.form"},
		{"create", "widgets.form"},
		{"update", "widgets.form"},
		{"delete", ""}, // no view claims delete
	}
	for _, c := range cases {
		if got := schemaViewFor(views, "widgets.widget", c.crudAction); got != c.want {
			t.Errorf("schemaViewFor(%q) = %q, want %q", c.crudAction, got, c.want)
		}
	}

	if got := schemaViewFor(views, "unknown.model", "list"); got != "" {
		t.Errorf("schemaViewFor for unknown model = %q, want \"\"", got)
	}
}

// TestModuleRegistry_Update_SchemaResponseCachedPerSnapshot is goerp#591's
// own regression guard: GET /_meta/schema's response is built once per
// published snapshot (here, the same *SchemaResponse pointer for two
// Snapshot() reads against one publish), and a later Update produces a
// distinct, correctly updated response — the cache is snapshot-scoped, not
// process-lifetime.
func TestModuleRegistry_Update_SchemaResponseCachedPerSnapshot(t *testing.T) {
	r := &ModuleRegistry{}
	modules1 := map[string]*module.LoadedModule{
		"contacts": {
			Status:         module.StatusReady,
			Manifest:       manifest.Manifest{Type: "standard"},
			ExplicitRoutes: []engine.RouteDeclaration{{Method: "GET", Path: "/", Auth: "required"}},
		},
	}
	if _, err := r.Update(modules1); err != nil {
		t.Fatalf("Update() error = %v", err)
	}

	resp1 := r.Snapshot().SchemaResponse()
	resp2 := r.Snapshot().SchemaResponse()
	if resp1 != resp2 {
		t.Errorf("SchemaResponse() returned distinct pointers for two reads of the same snapshot")
	}
	if _, ok := resp1.Modules["contacts"]; !ok {
		t.Fatalf("modules missing \"contacts\", got %v", resp1.Modules)
	}

	modules2 := map[string]*module.LoadedModule{
		"contacts": {
			Status:         module.StatusReady,
			Manifest:       manifest.Manifest{Type: "standard"},
			ExplicitRoutes: []engine.RouteDeclaration{{Method: "GET", Path: "/other", Auth: "required"}},
		},
	}
	if _, err := r.Update(modules2); err != nil {
		t.Fatalf("Update() error = %v", err)
	}

	resp3 := r.Snapshot().SchemaResponse()
	if resp3 == resp1 {
		t.Error("SchemaResponse() returned the same pointer after a new Update")
	}
	if got := resp3.Modules["contacts"].Routes[0].Path; got != "/contacts/other" {
		t.Errorf("updated response route path = %q, want /contacts/other", got)
	}
}
