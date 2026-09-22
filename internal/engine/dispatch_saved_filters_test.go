package engine

import (
	"bytes"
	"encoding/json/v2"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/auth/authcheck"
	"github.com/djangbahevans/goerp/internal/engine/route"
	"github.com/djangbahevans/goerp/internal/engine/savedfilters"
	tenantresolve "github.com/djangbahevans/goerp/internal/engine/tenant/resolve"
)

// dispatchSavedFiltersFixture wires up everything the
// /_meta/saved-filters handlers need — a real Engine (savedFiltersStore
// only, every other field left zero-value, the same "just enough for
// this handler" posture dispatchSharesFixture uses for record_shares) and
// a fixture tenant schema with the saved_filters table bootstrapped.
type dispatchSavedFiltersFixture struct {
	e        *Engine
	slug     string
	tenantID string
	userID   string
}

func newDispatchSavedFiltersFixture(t *testing.T) *dispatchSavedFiltersFixture {
	t.Helper()
	conn := openDispatchORMTestDB(t)

	slug := fmt.Sprintf("dispatchsavedfilterstest%d", time.Now().UnixNano())
	if _, err := conn.Exec("CREATE SCHEMA tenant_" + slug); err != nil {
		t.Fatalf("create fixture schema: %v", err)
	}
	t.Cleanup(func() { _, _ = conn.Exec("DROP SCHEMA tenant_" + slug + " CASCADE") })

	store := savedfilters.NewStore(conn)
	if err := store.Bootstrap(t.Context(), slug); err != nil {
		t.Fatalf("savedfilters Bootstrap() error: %v", err)
	}

	return &dispatchSavedFiltersFixture{
		e:        &Engine{savedFiltersStore: store},
		slug:     slug,
		tenantID: "00000000-0000-0000-0000-000000000001",
		userID:   "00000000-0000-0000-0000-0000000000aa",
	}
}

// request builds an httptest request carrying the same tenant/auth
// context values tenantResolutionMiddleware/authMiddleware would have
// already stashed by the time these handlers run — same shape
// dispatchSharesFixture.request uses, parameterized by which user is
// making the call (own-rows-only tests need a second, different caller).
func (f *dispatchSavedFiltersFixture) request(method, target, callerUserID string, body []byte, pathParams map[string]string) *http.Request {
	var r *http.Request
	if body != nil {
		r = httptest.NewRequest(method, target, bytes.NewReader(body))
	} else {
		r = httptest.NewRequest(method, target, nil)
	}

	ctx := withTenantContext(r.Context(), &tenantresolve.TenantContext{TenantID: f.tenantID, Slug: f.slug})
	ctx = withAuthContext(ctx, &authcheck.AuthContext{IsAuthenticated: true, UserID: callerUserID})
	if pathParams != nil {
		ctx = route.WithParams(ctx, pathParams)
	}
	return r.WithContext(ctx)
}

func TestDispatchSavedFiltersCreateRoute_Success(t *testing.T) {
	f := newDispatchSavedFiltersFixture(t)

	body, _ := json.Marshal(map[string]any{
		"view_name":    "contacts_list",
		"label":        "Active only",
		"query_string": "filter[is_active]=true",
		"is_default":   true,
	})
	w := httptest.NewRecorder()
	f.e.dispatchSavedFiltersCreateRoute(w, f.request(http.MethodPost, "/_meta/saved-filters", f.userID, body, nil))

	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body: %s", w.Code, w.Body.String())
	}
	var resp savedFilterResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.ViewName != "contacts_list" || resp.Label != "Active only" ||
		resp.QueryString != "filter[is_active]=true" || !resp.IsDefault {
		t.Errorf("resp = %+v, want fields to match request", resp)
	}

	filters, err := f.e.savedFiltersStore.ListForUserAndView(t.Context(), f.slug, f.userID, "contacts_list")
	if err != nil {
		t.Fatalf("ListForUserAndView() error: %v", err)
	}
	if len(filters) != 1 {
		t.Fatalf("saved_filters rows = %d, want 1", len(filters))
	}
}

func TestDispatchSavedFiltersCreateRoute_RejectsMissingFields(t *testing.T) {
	f := newDispatchSavedFiltersFixture(t)

	body, _ := json.Marshal(map[string]any{"view_name": "contacts_list"})
	w := httptest.NewRecorder()
	f.e.dispatchSavedFiltersCreateRoute(w, f.request(http.MethodPost, "/_meta/saved-filters", f.userID, body, nil))

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body: %s", w.Code, w.Body.String())
	}
	if code := decodeErrorCode(t, w); code != "invalid_request" {
		t.Errorf("error.code = %q, want invalid_request", code)
	}
}

func TestDispatchSavedFiltersListRoute_ReturnsOnlyTheCallersOwnFiltersForTheView(t *testing.T) {
	f := newDispatchSavedFiltersFixture(t)
	otherUserID := "00000000-0000-0000-0000-0000000000bb"

	if _, err := f.e.savedFiltersStore.Create(t.Context(), f.slug, f.userID, "contacts_list", "Mine", "filter[a]=1", false); err != nil {
		t.Fatalf("seed Create() error: %v", err)
	}
	if _, err := f.e.savedFiltersStore.Create(t.Context(), f.slug, otherUserID, "contacts_list", "Not mine", "filter[b]=2", false); err != nil {
		t.Fatalf("seed Create() error: %v", err)
	}
	if _, err := f.e.savedFiltersStore.Create(t.Context(), f.slug, f.userID, "orders_list", "Different view", "filter[c]=3", false); err != nil {
		t.Fatalf("seed Create() error: %v", err)
	}

	w := httptest.NewRecorder()
	f.e.dispatchSavedFiltersListRoute(w, f.request(http.MethodGet, "/_meta/saved-filters?view_name=contacts_list", f.userID, nil, nil))

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", w.Code, w.Body.String())
	}
	var resp struct {
		Data []savedFilterResponse `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(resp.Data) != 1 || resp.Data[0].Label != "Mine" {
		t.Errorf("data = %+v, want only the caller's own contacts_list filter", resp.Data)
	}
}

func TestDispatchSavedFiltersListRoute_RejectsMissingViewName(t *testing.T) {
	f := newDispatchSavedFiltersFixture(t)

	w := httptest.NewRecorder()
	f.e.dispatchSavedFiltersListRoute(w, f.request(http.MethodGet, "/_meta/saved-filters", f.userID, nil, nil))

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body: %s", w.Code, w.Body.String())
	}
}

func TestDispatchSavedFiltersUpdateRoute_RenamesTheFilter(t *testing.T) {
	f := newDispatchSavedFiltersFixture(t)
	sf, err := f.e.savedFiltersStore.Create(t.Context(), f.slug, f.userID, "contacts_list", "Original", "filter[a]=1", false)
	if err != nil {
		t.Fatalf("seed Create() error: %v", err)
	}

	body, _ := json.Marshal(map[string]any{"label": "Renamed"})
	w := httptest.NewRecorder()
	f.e.dispatchSavedFiltersUpdateRoute(w, f.request(http.MethodPatch, "/_meta/saved-filters/"+sf.ID, f.userID, body, map[string]string{"id": sf.ID}))

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", w.Code, w.Body.String())
	}
	var resp savedFilterResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Label != "Renamed" || resp.QueryString != "filter[a]=1" {
		t.Errorf("resp = %+v, want label renamed and query_string unchanged", resp)
	}
}

func TestDispatchSavedFiltersUpdateRoute_RejectsWhenCallerDoesNotOwnTheFilter(t *testing.T) {
	f := newDispatchSavedFiltersFixture(t)
	otherUserID := "00000000-0000-0000-0000-0000000000bb"
	sf, err := f.e.savedFiltersStore.Create(t.Context(), f.slug, otherUserID, "contacts_list", "Not mine", "filter[a]=1", false)
	if err != nil {
		t.Fatalf("seed Create() error: %v", err)
	}

	body, _ := json.Marshal(map[string]any{"label": "Hijacked"})
	w := httptest.NewRecorder()
	f.e.dispatchSavedFiltersUpdateRoute(w, f.request(http.MethodPatch, "/_meta/saved-filters/"+sf.ID, f.userID, body, map[string]string{"id": sf.ID}))

	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; body: %s", w.Code, w.Body.String())
	}
	if code := decodeErrorCode(t, w); code != "permission_denied" {
		t.Errorf("error.code = %q, want permission_denied", code)
	}
}

func TestDispatchSavedFiltersUpdateRoute_UnknownIDReturns404(t *testing.T) {
	f := newDispatchSavedFiltersFixture(t)

	body, _ := json.Marshal(map[string]any{"label": "Renamed"})
	w := httptest.NewRecorder()
	missingID := "99999999-9999-9999-9999-999999999999"
	f.e.dispatchSavedFiltersUpdateRoute(w, f.request(http.MethodPatch, "/_meta/saved-filters/"+missingID, f.userID, body, map[string]string{"id": missingID}))

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body: %s", w.Code, w.Body.String())
	}
}

func TestDispatchSavedFiltersDeleteRoute_RemovesTheFilter(t *testing.T) {
	f := newDispatchSavedFiltersFixture(t)
	sf, err := f.e.savedFiltersStore.Create(t.Context(), f.slug, f.userID, "contacts_list", "Temp", "filter[a]=1", false)
	if err != nil {
		t.Fatalf("seed Create() error: %v", err)
	}

	w := httptest.NewRecorder()
	f.e.dispatchSavedFiltersDeleteRoute(w, f.request(http.MethodDelete, "/_meta/saved-filters/"+sf.ID, f.userID, nil, map[string]string{"id": sf.ID}))

	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204; body: %s", w.Code, w.Body.String())
	}
	if _, err := f.e.savedFiltersStore.Get(t.Context(), f.slug, sf.ID); err != savedfilters.ErrNotFound {
		t.Errorf("Get() after delete error = %v, want ErrNotFound", err)
	}
}

func TestDispatchSavedFiltersDeleteRoute_RejectsWhenCallerDoesNotOwnTheFilter(t *testing.T) {
	f := newDispatchSavedFiltersFixture(t)
	otherUserID := "00000000-0000-0000-0000-0000000000bb"
	sf, err := f.e.savedFiltersStore.Create(t.Context(), f.slug, otherUserID, "contacts_list", "Not mine", "filter[a]=1", false)
	if err != nil {
		t.Fatalf("seed Create() error: %v", err)
	}

	w := httptest.NewRecorder()
	f.e.dispatchSavedFiltersDeleteRoute(w, f.request(http.MethodDelete, "/_meta/saved-filters/"+sf.ID, f.userID, nil, map[string]string{"id": sf.ID}))

	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; body: %s", w.Code, w.Body.String())
	}
	if code := decodeErrorCode(t, w); code != "permission_denied" {
		t.Errorf("error.code = %q, want permission_denied", code)
	}

	if _, err := f.e.savedFiltersStore.Get(t.Context(), f.slug, sf.ID); err != nil {
		t.Errorf("Get() after rejected delete error = %v, want the row to still exist", err)
	}
}

func TestDispatchSavedFiltersDeleteRoute_UnknownIDReturns404(t *testing.T) {
	f := newDispatchSavedFiltersFixture(t)

	missingID := "99999999-9999-9999-9999-999999999999"
	w := httptest.NewRecorder()
	f.e.dispatchSavedFiltersDeleteRoute(w, f.request(http.MethodDelete, "/_meta/saved-filters/"+missingID, f.userID, nil, map[string]string{"id": missingID}))

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body: %s", w.Code, w.Body.String())
	}
}
