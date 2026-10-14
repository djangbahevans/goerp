package engine

import (
	"context"
	"encoding/json/v2"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"github.com/djangbahevans/goerp/internal/engine/route"
)

type routeRecordChange struct {
	tenantID  string
	resource  string
	recordID  string
	operation string
}

type routeChangeRecorder struct {
	changes []routeRecordChange
}

func (p *routeChangeRecorder) RecordChanged(_ context.Context, tenantID, resource, recordID, operation string) {
	p.changes = append(p.changes, routeRecordChange{tenantID: tenantID, resource: resource, recordID: recordID, operation: operation})
}

func TestDispatchORMRoute_LiveChanges(t *testing.T) {
	f := newDispatchORMFixture(t)
	publisher := &routeChangeRecorder{}
	f.e.wasmRuntime.SetRecordChangePublisher(publisher)

	dispatch := func(method string, body []byte, entry *route.RouteEntry, id string, wantStatus int) []byte {
		t.Helper()
		w := httptest.NewRecorder()
		f.e.dispatchORMRoute(w, f.request(method, "/testmodule/widgets/"+id, body, entry, map[string]string{"id": id}))
		if w.Code != wantStatus {
			t.Fatalf("%s status = %d, want %d: %s", method, w.Code, wantStatus, w.Body.String())
		}
		return w.Body.Bytes()
	}
	created := dispatch(http.MethodPost, []byte(`{"name":"private field value","code":"LIVE"}`), f.entryCreate, "", http.StatusCreated)
	var record struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(created, &record); err != nil {
		t.Fatal(err)
	}
	if record.ID == "" {
		t.Fatal("create returned no record ID")
	}
	dispatch(http.MethodPut, []byte(`{"name":"updated private value"}`), f.entryUpdate, record.ID, http.StatusOK)
	dispatch(http.MethodPost, []byte(`{"name":"duplicate","code":"LIVE"}`), f.entryCreate, "", http.StatusConflict)
	dispatch(http.MethodDelete, nil, f.entryDelete, record.ID, http.StatusNoContent)
	want := []routeRecordChange{
		{tenantID: f.tenantID, resource: "testmodule.widget", operation: "created"},
		{tenantID: f.tenantID, resource: "testmodule.widget", recordID: record.ID, operation: "updated"},
		{tenantID: f.tenantID, resource: "testmodule.widget", recordID: record.ID, operation: "deleted"},
	}
	if !slices.Equal(publisher.changes, want) {
		t.Fatalf("route changes = %+v, want %+v", publisher.changes, want)
	}
}
