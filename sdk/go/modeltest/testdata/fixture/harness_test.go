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
