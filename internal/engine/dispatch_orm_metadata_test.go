package engine

import (
	"context"
	"encoding/json/v2"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/abi"
	"github.com/djangbahevans/goerp/internal/engine/manifest"
	"github.com/djangbahevans/goerp/internal/engine/module"
	"github.com/djangbahevans/goerp/internal/engine/schema"
	"github.com/djangbahevans/goerp/internal/engine/tenantschema"
	"github.com/djangbahevans/goerp/sdk/go/model"
)

func decodeMetadataResponse(t *testing.T, w *httptest.ResponseRecorder, status int) map[string]any {
	t.Helper()

	if w.Code != status {
		t.Fatalf("status = %d, want %d; body: %s", w.Code, status, w.Body.String())
	}
	var record map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &record); err != nil {
		t.Fatal(err)
	}
	return record
}

func TestDispatchORMRoute_UpdateMaintainsTimestampWithoutAudit(t *testing.T) {
	f := newDispatchORMFixture(t)
	w := httptest.NewRecorder()
	f.e.dispatchORMRoute(w, f.request(http.MethodPost, "/testmodule/widgets", []byte(`{"name":"Widget"}`), f.entryCreate, nil))
	created := decodeMetadataResponse(t, w, http.StatusCreated)
	id := created["id"].(string)

	w = httptest.NewRecorder()
	f.e.dispatchORMRoute(w, f.request(http.MethodPut, "/testmodule/widgets/"+id, []byte(`{"name":"Renamed"}`), f.entryUpdate, map[string]string{"id": id}))
	updated := decodeMetadataResponse(t, w, http.StatusOK)
	createdAt, err := time.Parse(time.RFC3339Nano, created["updated_at"].(string))
	if err != nil {
		t.Fatal(err)
	}
	updatedAt, err := time.Parse(time.RFC3339Nano, updated["updated_at"].(string))
	if err != nil {
		t.Fatal(err)
	}
	if !updatedAt.After(createdAt) || updated["created_at"] != created["created_at"] {
		t.Fatalf("create timestamps = %v/%v, updated = %v/%v", created["created_at"], created["updated_at"], updated["created_at"], updated["updated_at"])
	}
}

func TestDispatchORMRoute_WorkflowTransitionMaintainsTimestampWithoutAudit(t *testing.T) {
	f := newDispatchWorkflowFixture(t)
	id := f.createOrder(t)
	table := tenantschema.Name(f.slug) + `."order"`
	var createdAt, updatedAt time.Time
	if err := f.e.primaryDB.QueryRowContext(t.Context(), "SELECT created_at, updated_at FROM "+table+" WHERE id = $1", id).
		Scan(&createdAt, &updatedAt); err != nil {
		t.Fatal(err)
	}

	w := httptest.NewRecorder()
	f.e.dispatchORMRoute(w, f.request(http.MethodPost, "/sales/orders/"+id+"/confirm", nil, f.entryConfirm, map[string]string{"id": id}))
	decodeMetadataResponse(t, w, http.StatusOK)

	var storedCreatedAt, storedUpdatedAt time.Time
	if err := f.e.primaryDB.QueryRowContext(t.Context(), "SELECT created_at, updated_at FROM "+table+" WHERE id = $1", id).
		Scan(&storedCreatedAt, &storedUpdatedAt); err != nil {
		t.Fatal(err)
	}
	if !storedCreatedAt.Equal(createdAt) || !storedUpdatedAt.After(updatedAt) {
		t.Fatalf("workflow timestamps before = %v/%v, after = %v/%v", createdAt, updatedAt, storedCreatedAt, storedUpdatedAt)
	}
}

func TestDispatchORMRoute_AuditedLinksEnforceIfMatch(t *testing.T) {
	f := newDispatchORMFixture(t)
	md := widgetModelDecl()
	md.Field("tag_ids", model.Many2Many("testmodule.widget",
		model.Through("widget_links"), model.M2MColumn1("widget_id"), model.M2MColumn2("tag_id")))
	if _, err := f.e.moduleRegistry.Update(map[string]*module.LoadedModule{
		"testmodule": {
			Status:       module.StatusReady,
			Manifest:     manifest.Manifest{Name: "testmodule", Type: "standard"},
			ModelDecls:   []model.ModelDeclaration{md},
			Capabilities: abi.CapDBRead | abi.CapDBWrite,
		},
	}); err != nil {
		t.Fatal(err)
	}
	tenantSchema := tenantschema.Name(f.slug)
	if _, err := f.e.primaryDB.ExecContext(t.Context(), "CREATE TABLE "+tenantSchema+`.widget_links (
		widget_id UUID REFERENCES `+tenantSchema+`.widget(id),
		tag_id UUID REFERENCES `+tenantSchema+`.widget(id),
		PRIMARY KEY (widget_id, tag_id)
	)`); err != nil {
		t.Fatal(err)
	}
	pool := schema.NewPool(f.e.primaryDB, 5*time.Second)
	if err := pool.Bootstrap(t.Context()); err != nil {
		t.Fatal(err)
	}
	sess, err := pool.BeginSync(t.Context(), f.tenantID, f.slug, "testmodule", &manifest.Manifest{Version: "1.0.0"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sess.Close(context.Background()) })
	if err := schema.NewSchemaDiffEngine(&schema.Config{}).SyncEtagTriggers(t.Context(), sess, []model.ModelDeclaration{md}, []manifest.AuditedTable{{Table: "widget"}}); err != nil {
		t.Fatal(err)
	}

	w := httptest.NewRecorder()
	f.e.dispatchORMRoute(w, f.request(http.MethodPost, "/testmodule/widgets", []byte(`{"name":"Tag"}`), f.entryCreate, nil))
	tagID := decodeMetadataResponse(t, w, http.StatusCreated)["id"].(string)
	w = httptest.NewRecorder()
	f.e.dispatchORMRoute(w, f.request(http.MethodPost, "/testmodule/widgets", []byte(`{"name":"Widget"}`), f.entryCreate, nil))
	created := decodeMetadataResponse(t, w, http.StatusCreated)
	id := created["id"].(string)
	body, err := json.Marshal(map[string]any{"tag_ids": []string{tagID}})
	if err != nil {
		t.Fatal(err)
	}
	r := f.request(http.MethodPut, "/testmodule/widgets/"+id, body, f.entryUpdate, map[string]string{"id": id})
	r.Header.Set("If-Match", created["etag"].(string))
	w = httptest.NewRecorder()
	f.e.dispatchORMRoute(w, r)
	updated := decodeMetadataResponse(t, w, http.StatusOK)
	if updated["etag"] == created["etag"] || updated["updated_at"] == created["updated_at"] {
		t.Fatalf("link-only update did not advance version: created = %v, updated = %v", created, updated)
	}

	r = f.request(http.MethodPut, "/testmodule/widgets/"+id, []byte(`{"name":"Stale"}`), f.entryUpdate, map[string]string{"id": id})
	r.Header.Set("If-Match", created["etag"].(string))
	w = httptest.NewRecorder()
	f.e.dispatchORMRoute(w, r)
	if w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "orm.etag_mismatch") {
		t.Fatalf("stale If-Match status = %d, body: %s", w.Code, w.Body.String())
	}
}
