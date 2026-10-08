package engine

import (
	"encoding/json/v2"
	"fmt"
	"maps"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/djangbahevans/goerp/internal/engine/auth/authcheck"
	"github.com/djangbahevans/goerp/internal/engine/user"
)

// adminDo is scheduledActivityFixture.do with the caller holding roles.
func (f *scheduledActivityFixture) adminDo(t *testing.T, roles []string, method, target string, body any, handler func(http.ResponseWriter, *http.Request), pathParams map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	var raw []byte
	if body != nil {
		raw, _ = json.Marshal(body)
	}
	r := f.requestAs(f.callerID, method, target, raw, pathParams)
	r = r.WithContext(withAuthContext(r.Context(), &authcheck.AuthContext{IsAuthenticated: true, UserID: f.callerID, RolesLive: roles}))
	w := httptest.NewRecorder()
	handler(w, r)
	return w
}

func (f *scheduledActivityFixture) adminPatchType(t *testing.T, key string, body any) *httptest.ResponseRecorder {
	t.Helper()
	return f.adminDo(t, []string{"admin"}, http.MethodPatch, "/admin/activity-types/"+key, body, f.e.dispatchAdminActivityTypeUpdateRoute, map[string]string{"key": key})
}

func (f *scheduledActivityFixture) adminCreateType(t *testing.T, body any) *httptest.ResponseRecorder {
	t.Helper()
	return f.adminDo(t, []string{"admin"}, http.MethodPost, "/admin/activity-types", body, f.e.dispatchAdminActivityTypeCreateRoute, nil)
}

func (f *scheduledActivityFixture) listTypes(t *testing.T, callerID string) []map[string]any {
	t.Helper()
	w := f.do(t, callerID, http.MethodGet, "/_meta/activity-types", nil, f.e.dispatchActivityTypeListRoute, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("GET /_meta/activity-types status = %d; body: %s", w.Code, w.Body.String())
	}
	return decodeDataList(t, w)
}

func decodeDataList(t *testing.T, w *httptest.ResponseRecorder) []map[string]any {
	t.Helper()
	var resp struct {
		Data []map[string]any `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode list: %v; body: %s", err, w.Body.String())
	}
	return resp.Data
}

func typeKeys(types []map[string]any) []string {
	out := make([]string, len(types))
	for i, typ := range types {
		out[i] = typ["key"].(string)
	}
	return out
}

func TestDispatchActivityTypes_ListResolvesLabelsForTheCallersLocale(t *testing.T) {
	f := newScheduledActivityFixture(t)
	w := f.adminCreateType(t, map[string]any{
		"key": "site_visit", "icon": "map-pin", "default_due_days": 2,
		"label":           map[string]string{"en": "Site visit", "fr": "Visite sur site"},
		"default_summary": map[string]string{"en": "Visit the site"},
	})
	if w.Code != http.StatusCreated {
		t.Fatalf("POST status = %d; body: %s", w.Code, w.Body.String())
	}

	types := f.listTypes(t, f.callerID)
	if got, want := typeKeys(types), []string{"call", "meeting", "email", "todo", "site_visit"}; !slices.Equal(got, want) {
		t.Fatalf("keys = %v, want %v", got, want)
	}
	visit := types[4]
	if visit["label"] != "Site visit" || visit["default_summary"] != "Visit the site" || visit["default_due_days"] != float64(2) || visit["icon"] != "map-pin" {
		t.Errorf("site_visit = %v", visit)
	}
	if types[0]["default_summary"] != nil || types[0]["archived"] != false {
		t.Errorf("call = %v, want no default summary, active", types[0])
	}

	if _, err := f.e.userStore.UpdateProfile(t.Context(), f.callerID, user.ProfileUpdate{Locale: user.NullableField{Set: true, Value: new("fr-GH")}}); err != nil {
		t.Fatalf("UpdateProfile() error: %v", err)
	}
	types = f.listTypes(t, f.callerID)
	// The seeded labels only carry the platform default here; the fallback
	// chain finds "fr" for fr-GH, then "en".
	if types[4]["label"] != "Visite sur site" || types[4]["default_summary"] != "Visit the site" || types[0]["label"] != "Call" {
		t.Errorf("fr-GH labels = %v, %v; want Visite sur site / Visit the site, Call", types[4], types[0])
	}
}

func TestDispatchActivityTypes_AdminRoutesNeedTheAdminRole(t *testing.T) {
	f := newScheduledActivityFixture(t)
	routes := []struct {
		method, target string
		handler        func(http.ResponseWriter, *http.Request)
		params         map[string]string
	}{
		{http.MethodGet, "/admin/activity-types", f.e.dispatchAdminActivityTypeListRoute, nil},
		{http.MethodPost, "/admin/activity-types", f.e.dispatchAdminActivityTypeCreateRoute, nil},
		{http.MethodPatch, "/admin/activity-types/call", f.e.dispatchAdminActivityTypeUpdateRoute, map[string]string{"key": "call"}},
		{http.MethodPut, "/admin/activity-types/order", f.e.dispatchAdminActivityTypeReorderRoute, nil},
		{http.MethodDelete, "/admin/activity-types/call", f.e.dispatchAdminActivityTypeDeleteRoute, map[string]string{"key": "call"}},
	}
	for _, rt := range routes {
		w := f.adminDo(t, []string{"user"}, rt.method, rt.target, map[string]any{}, rt.handler, rt.params)
		wantError(t, w, http.StatusForbidden, "forbidden", rt.method+" "+rt.target)
	}
}

func TestDispatchActivityTypes_AdminListCarriesMapsAndUsage(t *testing.T) {
	f := newScheduledActivityFixture(t)
	f.schedule(t, f.callerID, map[string]any{"type": "meeting"})

	w := f.adminDo(t, []string{"admin"}, http.MethodGet, "/admin/activity-types", nil, f.e.dispatchAdminActivityTypeListRoute, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("GET status = %d; body: %s", w.Code, w.Body.String())
	}
	types := decodeDataList(t, w)
	meeting := types[1]
	if meeting["usage_count"] != float64(1) || types[0]["usage_count"] != float64(0) {
		t.Errorf("usage = %v, %v; want 0, 1", types[0]["usage_count"], meeting["usage_count"])
	}
	if label, _ := meeting["label"].(map[string]any); label["en"] != "Meeting" {
		t.Errorf("meeting label = %v, want the untranslated map", meeting["label"])
	}
}

func TestDispatchActivityTypes_AdminWritesAndTheirErrors(t *testing.T) {
	f := newScheduledActivityFixture(t)
	valid := func(fields map[string]any) map[string]any {
		body := map[string]any{"key": "site_visit", "icon": "map-pin", "label": map[string]string{"en": "Site visit"}}
		maps.Copy(body, fields)
		return body
	}

	creates := map[string]map[string]any{
		"bad key":             {"key": "Site-Visit"},
		"long key":            {"key": "a" + strings.Repeat("b", 40)},
		"reserved key":        {"key": "order"},
		"no default locale":   {"label": map[string]string{"fr": "Visite"}},
		"empty label":         {"label": map[string]string{"en": "  "}},
		"long label":          {"label": map[string]string{"en": strings.Repeat("x", 61)}},
		"unknown locale tag":  {"label": map[string]string{"en": "Visit", "english": "Visit"}},
		"long default":        {"default_summary": map[string]string{"en": strings.Repeat("x", 201)}},
		"due days too big":    {"default_due_days": 366},
		"negative due days":   {"default_due_days": -1},
		"label not an object": {"label": "Site visit"},
	}
	for name, fields := range creates {
		wantError(t, f.adminCreateType(t, valid(fields)), http.StatusBadRequest, "invalid_request", name)
	}
	wantError(t, f.adminCreateType(t, valid(map[string]any{"icon": "not-an-icon"})), http.StatusBadRequest, "invalid_icon", "unknown icon")
	wantError(t, f.adminCreateType(t, valid(map[string]any{"key": "call"})), http.StatusConflict, "type_key_taken", "taken key")

	w := f.adminCreateType(t, valid(nil))
	if w.Code != http.StatusCreated {
		t.Fatalf("POST status = %d; body: %s", w.Code, w.Body.String())
	}
	created := decodeObject(t, w)
	if created["usage_count"] != float64(0) || created["archived"] != false || created["default_due_days"] != nil {
		t.Errorf("created = %v", created)
	}

	w = f.adminPatchType(t, "site_visit", map[string]any{"label": map[string]string{"en": "Visit", "fr": "Visite"}, "default_due_days": 3, "default_summary": map[string]string{"en": "Go"}})
	if w.Code != http.StatusOK {
		t.Fatalf("PATCH status = %d; body: %s", w.Code, w.Body.String())
	}
	w = f.adminPatchType(t, "site_visit", map[string]any{"default_due_days": nil, "default_summary": nil})
	patched := decodeObject(t, w)
	if w.Code != http.StatusOK || patched["default_due_days"] != nil || len(patched["default_summary"].(map[string]any)) != 0 {
		t.Errorf("PATCH clearing defaults = %d %v", w.Code, patched)
	}
	if label := patched["label"].(map[string]any); label["fr"] != "Visite" || label["en"] != "Visit" {
		t.Errorf("label = %v, want the patched map kept", label)
	}
	wantError(t, f.adminPatchType(t, "site_visit", map[string]any{"label": map[string]string{"fr": "Visite"}}), http.StatusBadRequest, "invalid_request", "PATCH label without default locale")
	wantError(t, f.adminPatchType(t, "site_visit", map[string]any{"icon": "nope-nope"}), http.StatusBadRequest, "invalid_icon", "PATCH unknown icon")
	wantError(t, f.adminPatchType(t, "nope", map[string]any{"icon": "phone"}), http.StatusNotFound, "not_found", "PATCH unknown key")
	wantError(t, f.adminPatchType(t, "Bad-Key", map[string]any{}), http.StatusNotFound, "not_found", "PATCH malformed key")

	// Reorder.
	order := func(keys any) *httptest.ResponseRecorder {
		return f.adminDo(t, []string{"admin"}, http.MethodPut, "/admin/activity-types/order", map[string]any{"keys": keys}, f.e.dispatchAdminActivityTypeReorderRoute, nil)
	}
	wantError(t, order([]string{"call", "meeting"}), http.StatusBadRequest, "invalid_request", "partial order")
	wantError(t, order(nil), http.StatusBadRequest, "invalid_request", "missing keys")
	w = order([]string{"site_visit", "todo", "email", "meeting", "call"})
	if w.Code != http.StatusOK {
		t.Fatalf("PUT order status = %d; body: %s", w.Code, w.Body.String())
	}
	if got := typeKeys(f.listTypes(t, f.callerID)); !slices.Equal(got, []string{"site_visit", "todo", "email", "meeting", "call"}) {
		t.Errorf("order after PUT = %v", got)
	}

	// Delete: in use, unknown, then an unused one.
	f.schedule(t, f.callerID, map[string]any{"type": "site_visit"})
	del := func(key string) *httptest.ResponseRecorder {
		return f.adminDo(t, []string{"admin"}, http.MethodDelete, "/admin/activity-types/"+key, nil, f.e.dispatchAdminActivityTypeDeleteRoute, map[string]string{"key": key})
	}
	wantError(t, del("site_visit"), http.StatusConflict, "type_in_use", "delete used type")
	wantError(t, del("nope"), http.StatusNotFound, "not_found", "delete unknown type")
	if w := del("email"); w.Code != http.StatusNoContent {
		t.Errorf("DELETE email status = %d; body: %s", w.Code, w.Body.String())
	}

	// Archive every type but one, then the last.
	for _, key := range []string{"site_visit", "todo", "meeting"} {
		if w := f.adminPatchType(t, key, map[string]any{"archived": true}); w.Code != http.StatusOK {
			t.Fatalf("archive %s status = %d; body: %s", key, w.Code, w.Body.String())
		}
	}
	wantError(t, f.adminPatchType(t, "call", map[string]any{"archived": true}), http.StatusConflict, "last_active_type", "archive last active")
}

func TestDispatchActivityTypes_LimitIsFiftyTypes(t *testing.T) {
	f := newScheduledActivityFixture(t)
	for i := range 46 {
		key := fmt.Sprintf("type_%d", i)
		if w := f.adminCreateType(t, map[string]any{"key": key, "icon": "star", "label": map[string]string{"en": key}}); w.Code != http.StatusCreated {
			t.Fatalf("POST %s status = %d; body: %s", key, w.Code, w.Body.String())
		}
	}
	w := f.adminCreateType(t, map[string]any{"key": "one_too_many", "icon": "star", "label": map[string]string{"en": "x"}})
	wantError(t, w, http.StatusConflict, "type_limit_reached", "51st type")
}

func TestDispatchScheduledActivity_TypeMustBeActiveUnlessUnchanged(t *testing.T) {
	f := newScheduledActivityFixture(t)
	id := f.schedule(t, f.callerID, map[string]any{"type": "email"})["id"].(string)
	if w := f.adminPatchType(t, "email", map[string]any{"archived": true}); w.Code != http.StatusOK {
		t.Fatalf("archive email status = %d; body: %s", w.Code, w.Body.String())
	}

	wantError(t, f.create(t, f.callerID, map[string]any{"type": "email"}), http.StatusBadRequest, "invalid_type", "create with archived type")
	if w := f.patch(t, f.callerID, id, map[string]any{"type": "email", "summary": "Still email"}); w.Code != http.StatusOK {
		t.Errorf("PATCH keeping the archived type status = %d; body: %s", w.Code, w.Body.String())
	}
	if w := f.patch(t, f.callerID, id, map[string]any{"type": "call"}); w.Code != http.StatusOK {
		t.Fatalf("PATCH to call status = %d; body: %s", w.Code, w.Body.String())
	}
	wantError(t, f.patch(t, f.callerID, id, map[string]any{"type": "email"}), http.StatusBadRequest, "invalid_type", "PATCH back to archived type")

	// A relabel shows on the existing activity's type without rewriting it.
	if w := f.adminPatchType(t, "call", map[string]any{"label": map[string]string{"en": "Phone call"}}); w.Code != http.StatusOK {
		t.Fatalf("relabel status = %d; body: %s", w.Code, w.Body.String())
	}
	activities := decodeDataList(t, f.listForRecord(t, f.recordID))
	if len(activities) != 1 || activities[0]["type"] != "call" {
		t.Fatalf("activities = %v, want the one call activity", activities)
	}
	if types := f.listTypes(t, f.callerID); types[0]["label"] != "Phone call" {
		t.Errorf("call label = %v, want Phone call", types[0]["label"])
	}
}
