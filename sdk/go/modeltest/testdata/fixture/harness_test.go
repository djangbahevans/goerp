package fixture

import (
	"encoding/json/v2"
	"reflect"
	"testing"
	"uuid"

	"github.com/djangbahevans/goerp/sdk/go/modeltest"
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

func TestStructResponseUsesJSONTags(t *testing.T) {
	h := modeltest.NewHarness(t)

	resp := h.GET("/widgets/typed-response")
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200; error=%v msg=%v", resp.StatusCode, resp.JSON("error.code"), resp.JSON("error.message"))
	}
	var got map[string]any
	resp.ParseJSON(&got)
	want := map[string]any{"id": "c1", "email": nil, "updated_at": "2026-01-02T03:04:05Z"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("body = %v, want %v", got, want)
	}
}

func TestSequenceFieldGetsConsecutiveValues(t *testing.T) {
	h := modeltest.NewHarness(t)

	var numbers []any
	for range 2 {
		resp := h.POST("/widgets/tickets", map[string]any{})
		if resp.StatusCode != 201 {
			t.Fatalf("status = %d, want 201; error=%v msg=%v", resp.StatusCode, resp.JSON("error.code"), resp.JSON("error.message"))
		}
		numbers = append(numbers, resp.JSON("number"))
	}
	// The bare counter until goerp#1168 formats it per the field's format.
	if want := []any{float64(1), float64(2)}; !reflect.DeepEqual(numbers, want) {
		t.Errorf("numbers = %v, want %v", numbers, want)
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

	h.DB.Seed("widgets", map[string]any{"id": uuid.New().String(), "name": "Seeded Widget"})
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

	id := uuid.New().String()
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
	if resp := h.Anonymous().POST("/widgets/gizmo-boxes/"+uuid.New().String()+"/ship", nil); resp.StatusCode != 401 {
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

	h.DB.Seed("widgets_gadget", map[string]any{"id": uuid.New().String(), "name": "Sprocket"})

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

	id := uuid.New().String()
	h.DB.Seed("widgets_gadget", map[string]any{"id": id, "name": "Sprocket", "state": "draft"})

	resp := h.POST("/widgets/gadgets/"+id+"/finish", nil)
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200; error=%v msg=%v", resp.StatusCode, resp.JSON("error.code"), resp.JSON("error.message"))
	}
	h.DB.AssertExists("widgets_gadget", map[string]any{"id": id, "state": "done"})
}

// TestKindProbe_FieldKindsRoundTrip pins goerp#960's own empirical
// findings as regression coverage: kindProbeRow (cmd/module/main.go)
// only compiles and decodes because each field's Go type already
// matches what orm's setFieldValue actually receives for that
// FieldKind — a future engine change to host.orm's msgpack decode that
// shifted one of these types would make this round trip fail, not
// silently drift.
func TestKindProbe_FieldKindsRoundTrip(t *testing.T) {
	h := modeltest.NewHarness(t)

	resp := h.POST("/widgets/kind-probe", map[string]any{})
	if resp.StatusCode != 201 {
		t.Fatalf("status = %d, want 201; error=%v msg=%v", resp.StatusCode, resp.JSON("error.code"), resp.JSON("error.message"))
	}

	if got := resp.JSON("decimal"); got != "123.45" {
		t.Errorf("decimal = %v, want \"123.45\" (model.Decimal decodes as a Go string)", got)
	}
	if got := resp.JSON("timestamp"); got != "2024-03-15T10:30:00Z" {
		t.Errorf("timestamp = %v, want 2024-03-15T10:30:00Z (model.TimestampTZ decodes as time.Time)", got)
	}
	if got := resp.JSON("date"); got != "2024-03-15T00:00:00Z" {
		t.Errorf("date = %v, want 2024-03-15T00:00:00Z (model.Date decodes as time.Time)", got)
	}
	if got := resp.JSON("time"); got != "13:45:00" {
		t.Errorf("time = %v, want \"13:45:00\" (model.Time decodes as a Go string)", got)
	}

	jsonbRaw, _ := resp.JSON("jsonb").(string)
	var jsonb map[string]any
	if err := json.Unmarshal([]byte(jsonbRaw), &jsonb); err != nil {
		t.Fatalf("jsonb = %q is not valid JSON: %v", jsonbRaw, err)
	}
	if jsonb["key"] != "value" || jsonb["n"] != float64(1) {
		t.Errorf("jsonb decoded = %+v, want {key: value, n: 1}", jsonb)
	}

	if got := resp.JSON("bytea"); got != "hello-bytes" {
		t.Errorf("bytea = %v, want \"hello-bytes\" (model.Bytea decodes as []byte)", got)
	}
	if got := resp.JSON("integer"); got != float64(42) {
		t.Errorf("integer = %v, want 42 (model.Integer decodes as int32)", got)
	}
	if got := resp.JSON("float"); got != float64(3.5) {
		t.Errorf("float = %v, want 3.5 (model.Float decodes as float64)", got)
	}
	if got := resp.JSON("priority"); got != "medium" {
		t.Errorf("priority = %v, want \"medium\" (model.Enum decodes into its generated named string type)", got)
	}
	if got := resp.JSON("has_note"); got != false {
		t.Errorf("has_note = %v, want false (optional_note was never set — a NULL optional column must decode without error)", got)
	}
	if got := resp.JSON("has_gadget_expansion"); got != false {
		t.Errorf("has_gadget_expansion = %v, want false (created_by_gadget_id was never set — no relation to expand)", got)
	}
}

// TestKindProbe_RelationRoundTrip is goerp#961's own AC for the
// generated Many2One shape: a real orm.SearchRead decodes
// *orm.RelationRef correctly for a row whose relation is set (resolving
// to the target's display_name) and for one whose relation is left
// unset (resolving to nil, not an error).
func TestKindProbe_RelationRoundTrip(t *testing.T) {
	h := modeltest.NewHarness(t)

	resp := h.POST("/widgets/kind-probe-relation", map[string]any{})
	if resp.StatusCode != 201 {
		t.Fatalf("status = %d, want 201; error=%v msg=%v", resp.StatusCode, resp.JSON("error.code"), resp.JSON("error.message"))
	}

	if got := resp.JSON("with_relation.gadget_id"); got == nil || got == "" {
		t.Errorf("with_relation.gadget_id = %v, want the created gadget's id", got)
	}
	if got := resp.JSON("with_relation.gadget_display_name"); got != "Acme" {
		t.Errorf("with_relation.gadget_display_name = %v, want \"Acme\"", got)
	}

	if got := resp.JSON("without_relation.gadget_id"); got != nil {
		t.Errorf("without_relation.gadget_id = %v, want nil (no relation set)", got)
	}
	if got := resp.JSON("without_relation.gadget_display_name"); got != nil {
		t.Errorf("without_relation.gadget_display_name = %v, want nil (*orm.RelationRef must decode to nil, not error, when there's nothing to expand)", got)
	}
}

// TestGadget_QueryAndDelete pins goerp#980's own AC: Gadget{}.Query()
// produces identical results to orm.From[models.Gadget](), and Delete()
// actually soft-deletes the record via orm.Unlink — Gadget has a
// deleted_at column (WithStandardFields), so Unlink sets it rather than
// removing the row outright.
func TestGadget_QueryAndDelete(t *testing.T) {
	h := modeltest.NewHarness(t)

	resp := h.POST("/widgets/gadget-query-delete", map[string]any{})
	if resp.StatusCode != 201 {
		t.Fatalf("status = %d, want 201; error=%v msg=%v", resp.StatusCode, resp.JSON("error.code"), resp.JSON("error.message"))
	}

	queryMethodIDs, _ := resp.JSON("query_method_ids").([]any)
	fromFuncIDs, _ := resp.JSON("from_func_ids").([]any)
	if len(queryMethodIDs) != 1 {
		t.Fatalf("query_method_ids = %v, want exactly one match", queryMethodIDs)
	}
	if !reflect.DeepEqual(queryMethodIDs, fromFuncIDs) {
		t.Errorf("Gadget{}.Query() ids = %v, orm.From[models.Gadget]() ids = %v, want identical", queryMethodIDs, fromFuncIDs)
	}

	if got := resp.JSON("soft_deleted"); got != true {
		t.Errorf("soft_deleted = %v, want true (Delete() should have set deleted_at)", got)
	}
}
