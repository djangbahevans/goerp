package engine

import (
	"encoding/json/v2"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/djangbahevans/goerp/internal/engine/enginetables"
	"github.com/djangbahevans/goerp/internal/engine/permission"
	"github.com/djangbahevans/goerp/internal/engine/registry"
	"github.com/djangbahevans/goerp/internal/engine/route"
	"go.opentelemetry.io/otel/trace/noop"
)

func newViewOverridesFixture(t *testing.T) *dispatchSavedFiltersFixture {
	t.Helper()
	f := newDispatchSavedFiltersFixture(t)
	for _, group := range enginetables.Groups {
		if len(group.Tables) == 1 && group.Tables[0].Name == "view_overrides" {
			for range 2 {
				if err := group.Create(t.Context(), f.e.primaryDB, f.slug); err != nil {
					t.Fatalf("bootstrap overrides: %v", err)
				}
			}
		}
	}
	f.e.moduleRegistry = &registry.ModuleRegistry{}
	if _, err := f.e.moduleRegistry.Update(nil); err != nil {
		t.Fatal(err)
	}
	return f
}

func (f *dispatchSavedFiltersFixture) overrideRequest(method, target, body string, allowed bool, id string) *http.Request {
	params := map[string]string{"id": id}
	r := f.request(method, target, f.userID, []byte(body), params)
	if allowed {
		auth := authFromContext(r.Context())
		index, _ := f.e.moduleRegistry.Snapshot().PermissionRegistry().Index(permission.ManageViewOverrides)
		auth.PermissionSet.Set(index)
	}
	return r
}

func TestDispatchViewOverrides_RoundTripAndUpsert(t *testing.T) {
	f := newViewOverridesFixture(t)
	create := func(value string, wantStatus int) viewOverrideResponse {
		t.Helper()
		body := `{"view_name":"contacts_form","element":"name","property":"label","value":` + value + `}`
		w := httptest.NewRecorder()
		f.e.dispatchViewOverridesRoute(w, f.overrideRequest(http.MethodPost, "/_meta/view-overrides", body, true, ""))
		if w.Code != wantStatus {
			t.Fatalf("POST = %d %s, want %d", w.Code, w.Body, wantStatus)
		}
		var out viewOverrideResponse
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		return out
	}
	first := create(`"Customer name"`, http.StatusCreated)
	second := create(`"Contact name"`, http.StatusOK)
	if first.ID == "" || second.ID != first.ID || second.UpdatedBy != f.userID || second.UpdatedAt.IsZero() {
		t.Fatalf("upsert = %+v, initial = %+v", second, first)
	}

	w := httptest.NewRecorder()
	f.e.dispatchViewOverridesRoute(w, f.overrideRequest(http.MethodGet, "/_meta/view-overrides?view_name=contacts_form", "", true, ""))
	var listed struct {
		Data []viewOverrideResponse `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &listed); err != nil {
		t.Fatal(err)
	}
	if w.Code != http.StatusOK || len(listed.Data) != 1 || string(listed.Data[0].Value) != `"Contact name"` {
		t.Fatalf("GET = %d %s", w.Code, w.Body)
	}

	other := newViewOverridesFixture(t)
	w = httptest.NewRecorder()
	other.e.dispatchViewOverridesRoute(w, other.overrideRequest(http.MethodDelete, "/_meta/view-overrides/"+first.ID, "", true, first.ID))
	if w.Code != http.StatusNotFound {
		t.Fatalf("foreign tenant DELETE = %d %s", w.Code, w.Body)
	}

	r := f.overrideRequest(http.MethodDelete, "/_meta/view-overrides/"+first.ID, "", true, first.ID)
	authFromContext(r.Context()).UserID = "00000000-0000-0000-0000-0000000000bb"
	w = httptest.NewRecorder()
	f.e.dispatchViewOverridesRoute(w, r)
	if w.Code != http.StatusNoContent {
		t.Fatalf("another permitted user DELETE = %d %s", w.Code, w.Body)
	}
	w = httptest.NewRecorder()
	f.e.dispatchViewOverridesRoute(w, f.overrideRequest(http.MethodGet, "/_meta/view-overrides?view_name=contacts_form", "", true, ""))
	if w.Code != http.StatusOK || w.Body.String() != "{\"data\":[]}" {
		t.Fatalf("empty GET = %d %s", w.Code, w.Body)
	}
}

func TestDispatchViewOverrides_RequiresPermissionForEveryOperation(t *testing.T) {
	f := newViewOverridesFixture(t)
	for _, method := range []string{http.MethodGet, http.MethodPost, http.MethodDelete} {
		t.Run(method, func(t *testing.T) {
			w := httptest.NewRecorder()
			f.e.dispatchViewOverridesRoute(w, f.overrideRequest(method, "/_meta/view-overrides?view_name=contacts_form", `{}`, false, "11111111-1111-1111-1111-111111111111"))
			if w.Code != http.StatusForbidden || decodeErrorCode(t, w) != "permission_denied" {
				t.Fatalf("status = %d %s", w.Code, w.Body)
			}
		})
	}
}

func TestDispatchViewOverrides_ValidatesRequestsAndFiltersByView(t *testing.T) {
	f := newViewOverridesFixture(t)
	for property, value := range map[string]string{
		"label":       `"Name"`,
		"hidden":      `true`,
		"required":    `false`,
		"order":       `2`,
		"help_text":   `"Full name"`,
		"placeholder": `"Enter name"`,
	} {
		body := `{"view_name":"contacts_form","element":"name","property":"` + property + `","value":` + value + `}`
		w := httptest.NewRecorder()
		f.e.dispatchViewOverridesRoute(w, f.overrideRequest(http.MethodPost, "/_meta/view-overrides", body, true, ""))
		if w.Code != http.StatusCreated {
			t.Fatalf("property %s = %d %s", property, w.Code, w.Body)
		}
	}
	for _, body := range []string{
		`{`,
		`[]`,
		`{"view_name":"contacts_form","element":"name","property":"access","value":true}`,
		`{"view_name":"contacts_form","element":"name","property":"label"}`,
		`{"element":"name","property":"label","value":"Name"}`,
		`{"view_name":"contacts_form","property":"label","value":"Name"}`,
	} {
		w := httptest.NewRecorder()
		f.e.dispatchViewOverridesRoute(w, f.overrideRequest(http.MethodPost, "/_meta/view-overrides", body, true, ""))
		if w.Code != http.StatusBadRequest {
			t.Fatalf("body %s = %d %s", body, w.Code, w.Body)
		}
	}
	w := httptest.NewRecorder()
	f.e.dispatchViewOverridesRoute(w, f.overrideRequest(http.MethodGet, "/_meta/view-overrides", "", true, ""))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("missing view = %d %s", w.Code, w.Body)
	}
	w = httptest.NewRecorder()
	f.e.dispatchViewOverridesRoute(w, f.overrideRequest(http.MethodGet, "/_meta/view-overrides?view_name=other_form", "", true, ""))
	if w.Code != http.StatusOK || w.Body.String() != "{\"data\":[]}" {
		t.Fatalf("other view = %d %s", w.Code, w.Body)
	}
}

func TestDispatchViewOverrides_MiddlewareEnforcesPermission(t *testing.T) {
	f := newChainFixture(t)
	e := &Engine{primaryDB: f.conn, moduleRegistry: f.reg}
	builtins := map[string]http.Handler{"GET /_meta/view-overrides": http.HandlerFunc(e.dispatchViewOverridesRoute)}
	rateLimit := route.RateLimitConfig{
		Requests:      10000,
		WindowSeconds: 60,
		Scope:         "ip",
	}
	h := buildChain(e, f.reg, builtins, nil, f.resolver, f.checker, noop.NewTracerProvider().Tracer("test"), f.cacheClient, rateLimit)
	req := httptest.NewRequest(http.MethodGet, "/_meta/view-overrides?view_name=contacts_form", nil)
	req.Host = f.domain
	req.Header.Set("Authorization", "Bearer "+f.issueToken(t))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("ungranted permission = %d %s", w.Code, w.Body)
	}
}
