package engine

import (
	"encoding/json/v2"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/djangbahevans/goerp/internal/engine/route"
)

func TestDispatchORMRoute_SoftDeletedRecordIsNotFound(t *testing.T) {
	f := newDispatchORMFixture(t)
	w := httptest.NewRecorder()
	f.e.dispatchORMRoute(w, f.request(http.MethodPost, "/testmodule/widgets", []byte(`{"name":"Delete me"}`), f.entryCreate, nil))
	if w.Code != http.StatusCreated {
		t.Fatalf("create status = %d: %s", w.Code, w.Body.String())
	}

	var record struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &record); err != nil || record.ID == "" {
		t.Fatalf("created record = %s, error = %v", w.Body.String(), err)
	}

	path := "/testmodule/widgets/" + record.ID
	params := map[string]string{"id": record.ID}
	w = httptest.NewRecorder()
	f.e.dispatchORMRoute(w, f.request(http.MethodDelete, path, nil, f.entryDelete, params))
	if w.Code != http.StatusNoContent {
		t.Fatalf("delete status = %d: %s", w.Code, w.Body.String())
	}

	for _, tt := range []struct {
		method string
		entry  *route.RouteEntry
		body   []byte
	}{
		{http.MethodGet, f.entryGet, nil},
		{http.MethodPatch, f.entryUpdate, []byte(`{"name":"Restore me"}`)},
		{http.MethodDelete, f.entryDelete, nil},
	} {
		w := httptest.NewRecorder()
		f.e.dispatchORMRoute(w, f.request(tt.method, path, tt.body, tt.entry, params))
		if w.Code != http.StatusNotFound {
			t.Errorf("%s status = %d, want 404: %s", tt.method, w.Code, w.Body.String())
		}
	}

	w = httptest.NewRecorder()
	f.e.dispatchORMRoute(w, f.request(http.MethodGet, "/testmodule/widgets", nil, f.entryList, nil))
	if w.Code != http.StatusOK {
		t.Fatalf("list status = %d: %s", w.Code, w.Body.String())
	}

	var list struct {
		Data []map[string]any `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &list); err != nil || len(list.Data) != 0 {
		t.Fatalf("list = %s, error = %v", w.Body.String(), err)
	}
}
