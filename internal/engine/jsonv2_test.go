package engine

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The next two exercise encoding/json/v2's stricter decode defaults
// (goerp#531) through the real ORM create route (decodeJSONRecord).

func TestDispatchORMRoute_Create_DuplicateObjectMemberNameIsBadRequest(t *testing.T) {
	f := newDispatchORMFixture(t)

	body := []byte(`{"id":"11111111-1111-1111-1111-111111111111","tenant_id":"00000000-0000-0000-0000-000000000001","name":"a","name":"b"}`)
	w := httptest.NewRecorder()
	f.e.dispatchORMRoute(w, f.request(http.MethodPost, "/testmodule/widgets", body, f.entryCreate, nil))

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body: %s", w.Code, w.Body.String())
	}
}

func TestDispatchORMRoute_Create_InvalidUTF8IsBadRequest(t *testing.T) {
	f := newDispatchORMFixture(t)

	body := []byte("{\"id\":\"11111111-1111-1111-1111-111111111111\",\"tenant_id\":\"00000000-0000-0000-0000-000000000001\",\"name\":\"\xff\xfe\"}")
	w := httptest.NewRecorder()
	f.e.dispatchORMRoute(w, f.request(http.MethodPost, "/testmodule/widgets", body, f.entryCreate, nil))

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body: %s", w.Code, w.Body.String())
	}
}

// encoding/json/v2's MarshalWrite doesn't escape HTML/JS-unsafe characters
// by default the way v1's Encoder did — writeJSON/writeRouteErrorDetails
// pass explicit options to keep that parity (goerp#531).
func TestDispatchORMRoute_Create_EscapesHTMLUnsafeCharacters(t *testing.T) {
	f := newDispatchORMFixture(t)

	body := []byte(`{"id":"11111111-1111-1111-1111-111111111111","tenant_id":"00000000-0000-0000-0000-000000000001","name":"<script>&</script>","code":"W-1"}`)
	w := httptest.NewRecorder()
	f.e.dispatchORMRoute(w, f.request(http.MethodPost, "/testmodule/widgets", body, f.entryCreate, nil))

	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body: %s", w.Code, w.Body.String())
	}
	wantEscaped := "\\u003cscript\\u003e\\u0026\\u003c/script\\u003e"
	if !strings.Contains(w.Body.String(), wantEscaped) {
		t.Errorf("body = %s, want it to contain %s", w.Body.String(), wantEscaped)
	}
}

func TestWriteRouteErrorDetails_EscapesHTMLUnsafeCharacters(t *testing.T) {
	w := httptest.NewRecorder()
	writeRouteError(w, http.StatusBadRequest, "invalid_request", "<script>&</script>")

	wantEscaped := "\\u003cscript\\u003e\\u0026\\u003c/script\\u003e"
	if !strings.Contains(w.Body.String(), wantEscaped) {
		t.Errorf("body = %s, want it to contain %s", w.Body.String(), wantEscaped)
	}
}
