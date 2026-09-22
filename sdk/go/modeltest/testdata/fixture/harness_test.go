package fixture

import (
	"testing"

	"github.com/djangbahevans/goerp/sdk/go/modeltest"
	"github.com/google/uuid"
)

func TestPing(t *testing.T) {
	h := modeltest.NewHarness(t)

	resp := h.GET("/widgets/ping")
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200; error=%v msg=%v", resp.StatusCode, resp.JSON("error.code"), resp.JSON("error.message"))
	}
	if got := resp.JSON("status"); got != "ok" {
		t.Fatalf("status field = %v, want ok", got)
	}
}

func TestPOST_NilSliceAndMapReachHandlerAsEmptyNotNull(t *testing.T) {
	h := modeltest.NewHarness(t)

	type echoBody struct {
		Tags []string          `json:"tags"`
		Meta map[string]string `json:"meta"`
	}
	resp := h.POST("/widgets/echo", echoBody{})
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200; error=%v msg=%v", resp.StatusCode, resp.JSON("error.code"), resp.JSON("error.message"))
	}
	if got := resp.JSON("tags_is_nil"); got != false {
		t.Errorf("tags_is_nil = %v, want false", got)
	}
	if got := resp.JSON("meta_is_nil"); got != false {
		t.Errorf("meta_is_nil = %v, want false", got)
	}
}

func TestCreateWidget_InsertsRowAndEmitsEvent(t *testing.T) {
	h := modeltest.NewHarness(t)

	resp := h.POST("/widgets", map[string]any{"name": "Acme Widget"})
	if resp.StatusCode != 201 {
		t.Fatalf("status = %d, want 201, error=%v msg=%v", resp.StatusCode, resp.JSON("error.code"), resp.JSON("error.message"))
	}
	id, _ := resp.JSON("id").(string)
	if id == "" {
		t.Fatalf("expected a non-empty id in response, got %v", resp.JSON("id"))
	}

	h.DB.AssertExists("widgets", map[string]any{"id": id, "name": "Acme Widget"})

	h.Events.AssertEmitted("widgets.widget.created")
	evt := h.Events.Last("widgets.widget.created")
	if evt.Payload["widget_id"] != id {
		t.Fatalf("event payload widget_id = %v, want %v", evt.Payload["widget_id"], id)
	}
	if !evt.WasTransactional {
		t.Fatalf("expected WasTransactional = true (emitted via EmitTx)")
	}

	list := h.GET("/widgets")
	items := list.JSONArray("data")
	if len(items) != 1 || items[0]["name"] != "Acme Widget" {
		t.Fatalf("unexpected list response: %+v", items)
	}

	var row struct {
		ID   string `db:"id"`
		Name string `db:"name"`
	}
	h.DB.QueryOne(&row, "SELECT id, name FROM widgets WHERE id = $1", id)
	if row.Name != "Acme Widget" {
		t.Fatalf("QueryOne name = %q, want %q", row.Name, "Acme Widget")
	}
}

func TestSeed_ThenAssertExists(t *testing.T) {
	h := modeltest.NewHarness(t)

	h.DB.Seed("widgets", map[string]any{"id": uuid.NewString(), "name": "Seeded Widget"})
	h.DB.AssertExists("widgets", map[string]any{"name": "Seeded Widget"})
	h.DB.AssertNotExists("widgets", map[string]any{"name": "Nonexistent Widget"})
	h.DB.AssertCount("widgets", 1, "")
	h.DB.AssertColumnExists("widgets", "name")
	h.DB.AssertColumnExists("widgets", "tenant_id")
}

func TestAnonymous_PingStillWorks(t *testing.T) {
	h := modeltest.NewHarness(t)

	resp := h.Anonymous().GET("/widgets/ping")
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
}

func TestActionOverridesEnableOpsRouteOfModelWithLabelPlural(t *testing.T) {
	h := modeltest.NewHarness(t)

	resp := h.GET("/widgets/gizmo-boxes")
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200; error=%v msg=%v", resp.StatusCode, resp.JSON("error.code"), resp.JSON("error.message"))
	}
	if got := resp.JSON("served_by"); got != "module" {
		t.Fatalf("served_by = %v, want module — the engine's EnableOps list route answered instead of the module's handler", got)
	}
	if got := resp.JSON("action"); got != "list" {
		t.Fatalf("action = %v, want list", got)
	}
}

func TestCustomActionIsExposedAtDerivedPathAndReachesItsHandler(t *testing.T) {
	h := modeltest.NewHarness(t)

	id := uuid.NewString()
	resp := h.POST("/widgets/gizmo-boxes/"+id+"/ship", nil)
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200; error=%v msg=%v", resp.StatusCode, resp.JSON("error.code"), resp.JSON("error.message"))
	}
	if resp.JSON("id") != id || resp.JSON("model") != "widgets.gizmo" || resp.JSON("action") != "ship" {
		t.Fatalf("response = id:%v model:%v action:%v, want id:%s model:widgets.gizmo action:ship",
			resp.JSON("id"), resp.JSON("model"), resp.JSON("action"), id)
	}
}

func TestActionRouteRequiresAuthenticationByDefault(t *testing.T) {
	h := modeltest.NewHarness(t)

	if resp := h.Anonymous().GET("/widgets/gizmo-boxes"); resp.StatusCode != 401 {
		t.Fatalf("anonymous list action status = %d, want 401", resp.StatusCode)
	}
	if resp := h.Anonymous().POST("/widgets/gizmo-boxes/"+uuid.NewString()+"/ship", nil); resp.StatusCode != 401 {
		t.Fatalf("anonymous custom action status = %d, want 401", resp.StatusCode)
	}
}

func TestCollectionActionUsesDeclaredMethod(t *testing.T) {
	h := modeltest.NewHarness(t)

	resp := h.PUT("/widgets/gizmo-boxes/restock", nil)
	if resp.StatusCode != 200 || resp.JSON("action") != "restock" {
		t.Fatalf("PUT status = %d action = %v, want 200 restock", resp.StatusCode, resp.JSON("action"))
	}
	if resp := h.POST("/widgets/gizmo-boxes/restock", nil); resp.StatusCode == 200 {
		t.Fatal("POST reached a collection action declared with PUT")
	}
}

func TestRecordActionRejectsANonUUIDID(t *testing.T) {
	h := modeltest.NewHarness(t)

	if resp := h.POST("/widgets/gizmo-boxes/not-a-uuid/ship", nil); resp.StatusCode != 400 {
		t.Fatalf("status = %d, want 400 for a non-UUID id", resp.StatusCode)
	}
}

func TestEnableOpsRoutesAreServedByTheEngine(t *testing.T) {
	h := modeltest.NewHarness(t)

	h.DB.Seed("widgets_gadget", map[string]any{"id": uuid.NewString(), "name": "Sprocket"})

	list := h.GET("/widgets/gadgets")
	if list.StatusCode != 200 {
		t.Fatalf("list status = %d, want 200; error=%v msg=%v", list.StatusCode, list.JSON("error.code"), list.JSON("error.message"))
	}
	if items := list.JSONArray("data"); len(items) != 1 || items[0]["name"] != "Sprocket" {
		t.Fatalf("list data = %+v, want the seeded gadget", items)
	}

	created := h.POST("/widgets/gadgets", map[string]any{"name": "Flywheel"})
	if created.StatusCode != 201 {
		t.Fatalf("create status = %d, want 201; error=%v msg=%v", created.StatusCode, created.JSON("error.code"), created.JSON("error.message"))
	}
	id, _ := created.JSON("id").(string)
	h.DB.AssertExists("widgets_gadget", map[string]any{"id": id, "name": "Flywheel", "tenant_id": h.TenantID})

	got := h.GET("/widgets/gadgets/" + id)
	if got.StatusCode != 200 || got.JSON("name") != "Flywheel" {
		t.Fatalf("get status = %d name = %v, want 200 Flywheel", got.StatusCode, got.JSON("name"))
	}
}

func TestWorkflowTransitionRouteIsServedByTheEngine(t *testing.T) {
	h := modeltest.NewHarness(t)

	id := uuid.NewString()
	h.DB.Seed("widgets_gadget", map[string]any{"id": id, "name": "Sprocket", "state": "draft"})

	resp := h.POST("/widgets/gadgets/"+id+"/finish", nil)
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200; error=%v msg=%v", resp.StatusCode, resp.JSON("error.code"), resp.JSON("error.message"))
	}
	h.DB.AssertExists("widgets_gadget", map[string]any{"id": id, "state": "done"})
}
