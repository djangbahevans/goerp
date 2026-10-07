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
			got := schemaModelFrom(tt.md, nil)
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

func TestSchemaModelFrom_StaticDefaultValues(t *testing.T) {
	md := model.Define("shop.item").WithStandardFields().
		Field("status", model.Selection("draft", "done").Required().Default("'draft'")).
		Field("is_active", model.Boolean().Required().Default("true")).
		Field("stock", model.Integer().Default("0")).
		Field("opened_at", model.TimestampTZ().Default("now()")).
		Field("note", model.Text())

	fields := map[string]SchemaField{}
	for _, f := range schemaModelFrom(*md, nil).Fields {
		fields[f.Name] = f
	}

	for field, want := range map[string]any{"status": "draft", "is_active": true, "stock": int64(0)} {
		if got := fields[field].Default; got != want {
			t.Errorf("%s default = %#v, want %#v", field, got, want)
		}
	}
	for _, field := range []string{"opened_at", "id", "note"} {
		if got := fields[field].Default; got != nil {
			t.Errorf("%s default = %#v, want none for a computed or absent default", field, got)
		}
	}
	if !fields["opened_at"].HasDefault {
		t.Error("opened_at has_default = false, want true even without a static value")
	}

	out, err := json.Marshal(fields["status"])
	if err != nil {
		t.Fatalf("Marshal() error: %v", err)
	}
	if !strings.Contains(string(out), `"default":"draft"`) {
		t.Errorf("status JSON = %s, want default:draft", out)
	}
}

func TestSchemaModelFrom_CodegenFieldAttributes(t *testing.T) {
	md := model.Define("shop.item").WithStandardFields().
		Field("status", model.Selection("draft", "done").Required().Default("'draft'")).
		Field("size", model.Enum("item_size")).
		Field("total", model.Decimal(10, 2).Computed("compute_total").Store(true)).
		Field("note", model.Text())
	types := []model.TypeDeclaration{model.EnumType("other", "x"), model.EnumType("item_size", "s", "m", "l")}

	fields := map[string]SchemaField{}
	for _, f := range schemaModelFrom(*md, types).Fields {
		fields[f.Name] = f
	}

	if got := fields["status"]; !slices.Equal(got.SelectionValues, []string{"draft", "done"}) || !got.HasDefault || !got.Required || got.Readonly {
		t.Errorf("status = %+v, want selection values, has_default, required, writable", got)
	}
	if got := fields["size"].SelectionValues; !slices.Equal(got, []string{"s", "m", "l"}) {
		t.Errorf("size selection_values = %v, want the item_size enum's values", got)
	}
	if !fields["total"].Readonly {
		t.Error("computed total: readonly = false, want true")
	}
	if got := fields["id"]; !got.PrimaryKey || !got.Readonly || !got.HasDefault {
		t.Errorf("id = %+v, want primary_key, readonly, has_default", got)
	}

	out, err := json.Marshal(fields["note"])
	if err != nil {
		t.Fatalf("Marshal() error: %v", err)
	}
	if want := `{"name":"note","type":"text"}`; string(out) != want {
		t.Errorf("plain field JSON = %s, want %s (new attributes omitted when unset)", out, want)
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
		{"delete", ""},
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

func TestBuildSchemaResponse_CRUDRouteIdentity(t *testing.T) {
	tests := []struct {
		name     string
		override bool
	}{
		{name: "EnableOps"},
		{name: "reserved action", override: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			md := model.Define("contacts.contact").EnableOps(model.List)
			explicit := []engine.RouteDeclaration{{
				Method: "GET", Path: "/search", Auth: "required", Model: "contacts.contact", CRUDAction: "list",
			}}
			if tt.override {
				explicit = append(explicit, engine.RouteDeclaration{Model: "contacts.contact", Name: "list", CRUDAction: "list", Auth: "required"})
			}
			modules := map[string]*module.LoadedModule{"contacts": {
				Status: module.StatusReady, Manifest: manifest.Manifest{Type: "domain"},
				ModelDecls: []model.ModelDeclaration{*md}, ExplicitRoutes: explicit,
			}}

			table, err := buildRouteTable(modules)
			if err != nil {
				t.Fatalf("buildRouteTable() error: %v", err)
			}
			resp := buildSchemaResponse(modules, table, "")
			out, err := json.Marshal(resp.Modules["contacts"].Routes)
			if err != nil {
				t.Fatalf("marshal routes: %v", err)
			}
			var routes []SchemaRoute
			if err := json.Unmarshal(out, &routes); err != nil {
				t.Fatalf("unmarshal routes: %v", err)
			}

			if len(routes) != 2 {
				t.Fatalf("routes = %+v, want one CRUD route and one raw binding", routes)
			}
			for _, r := range routes {
				if r.Model != "contacts.contact" || r.CrudAction != "list" {
					t.Errorf("route = %+v, want the model and list binding", r)
				}
				switch r.Path {
				case "/contacts/search":
					if r.EngineNative || r.Name != "" {
						t.Errorf("raw route = %+v, want neither engine_native nor name", r)
					}
				case "/contacts/contacts":
					if r.EngineNative != !tt.override || (r.Name == "list") != tt.override {
						t.Errorf("CRUD route = %+v, want engine_native=%v, reserved name=%v", r, !tt.override, tt.override)
					}
				default:
					t.Errorf("unexpected route path %q", r.Path)
				}
			}
		})
	}
}

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

func TestBuildSchemaResponse_NotificationTypes(t *testing.T) {
	sales := manifest.Manifest{Type: "standard", NotificationTypes: []manifest.NotificationType{{
		Name:              "order_confirmed",
		Label:             "Order Confirmed",
		Description:       "Sent when a sales order is confirmed",
		DefaultChannels:   []string{"in_app", "email"},
		AvailableChannels: []string{"in_app", "email", "push"},
		Templates:         map[string]string{"email": "notifications/order_confirmed/email.{locale}.html"},
	}}}
	modules := map[string]*module.LoadedModule{
		"sales":    {Status: module.StatusReady, Manifest: sales},
		"contacts": {Status: module.StatusReady, Manifest: manifest.Manifest{Type: "standard"}},
	}
	table, err := buildRouteTable(modules)
	if err != nil {
		t.Fatalf("buildRouteTable() error = %v", err)
	}
	resp := buildSchemaResponse(modules, table, "")

	out, err := json.Marshal(resp.Modules["sales"].NotificationTypes)
	if err != nil {
		t.Fatalf("Marshal() error: %v", err)
	}
	want := `[{"name":"order_confirmed","label":"Order Confirmed","description":"Sent when a sales order is confirmed","available_channels":["in_app","email","push"]}]`
	if string(out) != want {
		t.Errorf("sales notification_types = %s, want %s", out, want)
	}

	out, err = json.Marshal(resp.Modules["contacts"])
	if err != nil {
		t.Fatalf("Marshal() error: %v", err)
	}
	if !strings.Contains(string(out), `"notification_types":[]`) {
		t.Errorf("contacts module JSON lacks \"notification_types\":[]: %s", out)
	}
}

func TestBuildSchemaResponse_EngineNotificationTypes(t *testing.T) {
	table, err := buildRouteTable(map[string]*module.LoadedModule{})
	if err != nil {
		t.Fatalf("buildRouteTable() error = %v", err)
	}
	resp := buildSchemaResponse(map[string]*module.LoadedModule{}, table, "")

	var names []string
	for _, nt := range resp.EngineNotificationTypes {
		names = append(names, nt.Name)
	}
	want := []string{"activity_assigned", "activity_due", "record_mention", "record_message"}
	if !slices.Equal(names, want) {
		t.Errorf("engine_notification_types names = %v, want %v", names, want)
	}

	out, err := json.Marshal(resp.EngineNotificationTypes[0])
	if err != nil {
		t.Fatalf("Marshal() error: %v", err)
	}
	wantJSON := `{"name":"activity_assigned","label":"Activity assigned to you","description":"Sent when someone assigns you a scheduled activity","available_channels":["in_app","email","push"]}`
	if string(out) != wantJSON {
		t.Errorf("engine_notification_types[0] = %s, want %s", out, wantJSON)
	}
}

func TestComputeSchemaHash_NotificationTypes(t *testing.T) {
	hash := func(nt manifest.NotificationType) string {
		modules := map[string]*module.LoadedModule{"sales": {
			Status:   module.StatusReady,
			Manifest: manifest.Manifest{Type: "standard", NotificationTypes: []manifest.NotificationType{nt}},
		}}
		table, err := buildRouteTable(modules)
		if err != nil {
			t.Fatalf("buildRouteTable() error = %v", err)
		}
		return computeSchemaHash(modules, table)
	}
	base := manifest.NotificationType{Name: "order_confirmed", Label: "Order Confirmed", AvailableChannels: []string{"in_app", "email"}}

	relabelled := base
	relabelled.Label = "Order confirmed"
	if hash(base) == hash(relabelled) {
		t.Error("schema hash unchanged after a notification type's label changed")
	}

	retemplated := base
	retemplated.Templates = map[string]string{"email": "notifications/order_confirmed/email.{locale}.html"}
	if hash(base) != hash(retemplated) {
		t.Error("schema hash changed for a templates edit, which the schema response doesn't include")
	}
}
