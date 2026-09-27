package engine

import (
	"bytes"
	"encoding/json/v2"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
	"uuid"

	"github.com/djangbahevans/goerp/internal/engine/auth/authcheck"
	"github.com/djangbahevans/goerp/internal/engine/billing"
	"github.com/djangbahevans/goerp/internal/engine/notifications"
	"github.com/djangbahevans/goerp/internal/engine/route"
	"github.com/djangbahevans/goerp/internal/engine/tenant"
	tenantresolve "github.com/djangbahevans/goerp/internal/engine/tenant/resolve"
	"github.com/djangbahevans/goerp/internal/engine/tenantschema"
)

// dispatchNotifFixture is a tenant schema with a bootstrapped
// notifications table and an Engine carrying just the notification store.
// Notification rows are inserted directly, since nothing in the engine
// creates them yet.
type dispatchNotifFixture struct {
	e        *Engine
	slug     string
	tenantID string
	callerID string
	otherID  string
}

func newDispatchNotifFixture(t *testing.T) *dispatchNotifFixture {
	t.Helper()
	conn := openDispatchORMTestDB(t)
	slug := fmt.Sprintf("dispatchnotiftest%d", time.Now().UnixNano())
	schema := tenantschema.Name(slug)
	if _, err := conn.ExecContext(t.Context(), "CREATE SCHEMA "+schema); err != nil {
		t.Fatalf("create fixture schema: %v", err)
	}
	t.Cleanup(func() { _, _ = conn.Exec("DROP SCHEMA " + schema + " CASCADE") })

	store := notifications.NewStore(conn)
	if err := store.BootstrapFeed(t.Context(), slug); err != nil {
		t.Fatalf("BootstrapFeed() error: %v", err)
	}
	if err := store.BootstrapDeviceTokens(t.Context(), slug); err != nil {
		t.Fatalf("BootstrapDeviceTokens() error: %v", err)
	}
	if err := store.BootstrapPreferences(t.Context(), slug); err != nil {
		t.Fatalf("BootstrapPreferences() error: %v", err)
	}
	return &dispatchNotifFixture{
		e:        &Engine{primaryDB: conn, notificationStore: store},
		slug:     slug,
		tenantID: uuid.New().String(),
		callerID: uuid.New().String(),
		otherID:  uuid.New().String(),
	}
}

// insert adds a notification for userID, read when read is set, and
// returns its id. Successive inserts are successively newer.
func (f *dispatchNotifFixture) insert(t *testing.T, userID, title string, read bool) string {
	t.Helper()
	query := fmt.Sprintf(`
		INSERT INTO %s.notifications (tenant_id, user_id, type, module, title, read_at)
		VALUES ($1, $2, 'sales.order_confirmed', 'sales', $3, CASE WHEN $4 THEN NOW() END)
		RETURNING id
	`, tenantschema.Name(f.slug))
	var id string
	if err := f.e.primaryDB.QueryRowContext(t.Context(), query, f.tenantID, userID, title, read).Scan(&id); err != nil {
		t.Fatalf("insert notification: %v", err)
	}
	return id
}

func (f *dispatchNotifFixture) serve(handler http.HandlerFunc, callerID, method, target string, pathParams map[string]string) *httptest.ResponseRecorder {
	return f.serveBody(handler, callerID, method, target, nil, pathParams)
}

func (f *dispatchNotifFixture) serveBody(handler http.HandlerFunc, callerID, method, target string, body []byte, pathParams map[string]string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, target, bytes.NewReader(body))
	ctx := withTenantContext(r.Context(), &tenantresolve.TenantContext{TenantID: f.tenantID, Slug: f.slug})
	ctx = withAuthContext(ctx, &authcheck.AuthContext{IsAuthenticated: true, UserID: callerID})
	if pathParams != nil {
		ctx = route.WithParams(ctx, pathParams)
	}
	w := httptest.NewRecorder()
	handler(w, r.WithContext(ctx))
	return w
}

type notifFeedPage struct {
	Data []notifResponse `json:"data"`
	Meta notifFeedMeta   `json:"meta"`
}

func (f *dispatchNotifFixture) feed(t *testing.T, callerID string, query url.Values) notifFeedPage {
	t.Helper()
	w := f.serve(f.e.dispatchNotifFeedRoute, callerID, http.MethodGet, "/_notif/feed?"+query.Encode(), nil)
	if w.Code != http.StatusOK {
		t.Fatalf("GET /_notif/feed status = %d, want 200; body: %s", w.Code, w.Body.String())
	}
	var page notifFeedPage
	if err := json.Unmarshal(w.Body.Bytes(), &page); err != nil {
		t.Fatalf("decode feed: %v", err)
	}
	return page
}

func (f *dispatchNotifFixture) count(t *testing.T) int {
	t.Helper()
	w := f.serve(f.e.dispatchNotifCountRoute, f.callerID, http.MethodGet, "/_notif/count", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("GET /_notif/count status = %d, want 200; body: %s", w.Code, w.Body.String())
	}
	var body struct {
		Count int `json:"count"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode count: %v", err)
	}
	return body.Count
}

// assertUnread checks /_notif/count and the feed's meta.unread both equal want.
func (f *dispatchNotifFixture) assertUnread(t *testing.T, want int) {
	t.Helper()
	if got := f.count(t); got != want {
		t.Errorf("count = %d, want %d", got, want)
	}
	if got := f.feed(t, f.callerID, nil).Meta.Unread; got != want {
		t.Errorf("meta.unread = %d, want %d", got, want)
	}
}

func titles(items []notifResponse) []string {
	out := make([]string, len(items))
	for i, n := range items {
		out[i] = n.Title
	}
	return out
}

func TestDispatchNotifFeedRoute_ListsUnreadFirstThenNewestFirst(t *testing.T) {
	f := newDispatchNotifFixture(t)
	f.insert(t, f.callerID, "read-old", true)
	f.insert(t, f.callerID, "unread-old", false)
	f.insert(t, f.callerID, "read-new", true)
	f.insert(t, f.callerID, "unread-new", false)
	f.insert(t, f.otherID, "someone else's", false)

	page := f.feed(t, f.callerID, nil)
	want := []string{"unread-new", "unread-old", "read-new", "read-old"}
	if got := titles(page.Data); fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("feed = %v, want %v", got, want)
	}
	if page.Meta.HasMore || page.Meta.Cursor != nil {
		t.Errorf("meta = %+v, want no further page", page.Meta)
	}
	if page.Meta.Unread != 2 {
		t.Errorf("meta.unread = %d, want 2", page.Meta.Unread)
	}
	first := page.Data[0]
	if first.Type != "sales.order_confirmed" || first.Module != "sales" || first.ReadAt != nil || first.CreatedAt.IsZero() {
		t.Errorf("first item = %+v, want an unread sales.order_confirmed notification", first)
	}
	if page.Data[2].ReadAt == nil {
		t.Errorf("read item has no read_at")
	}

	unread := f.feed(t, f.callerID, url.Values{"unread": {"true"}})
	if got := titles(unread.Data); fmt.Sprint(got) != fmt.Sprint(want[:2]) {
		t.Errorf("unread feed = %v, want %v", got, want[:2])
	}
}

func TestDispatchNotifFeedRoute_PaginatesEveryNotificationExactlyOnce(t *testing.T) {
	f := newDispatchNotifFixture(t)
	var want []string
	var unread, read []string
	for i := range 7 {
		title := fmt.Sprintf("n%d", i)
		f.insert(t, f.callerID, title, i%2 == 0)
		if i%2 == 0 {
			read = append([]string{title}, read...)
		} else {
			unread = append([]string{title}, unread...)
		}
	}
	want = append(unread, read...)

	for _, limit := range []int{1, 2, 3, 7} {
		var got []string
		cursor := ""
		for pages := 0; ; pages++ {
			if pages > len(want) {
				t.Fatalf("limit %d: feed never ended", limit)
			}
			q := url.Values{"limit": {fmt.Sprint(limit)}}
			if cursor != "" {
				q.Set("cursor", cursor)
			}
			page := f.feed(t, f.callerID, q)
			if len(page.Data) > limit {
				t.Fatalf("limit %d: page has %d items", limit, len(page.Data))
			}
			got = append(got, titles(page.Data)...)
			if !page.Meta.HasMore {
				break
			}
			cursor = *page.Meta.Cursor
		}
		if fmt.Sprint(got) != fmt.Sprint(want) {
			t.Errorf("limit %d: paged feed = %v, want %v", limit, got, want)
		}
	}
}

func TestDispatchNotifFeedRoute_RejectsBadQuery(t *testing.T) {
	f := newDispatchNotifFixture(t)
	for _, q := range []string{"limit=0", "limit=101", "limit=x", "cursor=nope", "cursor=u.not-a-uuid", "cursor=" + uuid.New().String(), "unread=maybe"} {
		w := f.serve(f.e.dispatchNotifFeedRoute, f.callerID, http.MethodGet, "/_notif/feed?"+q, nil)
		if w.Code != http.StatusBadRequest {
			t.Errorf("%s: status = %d, want 400", q, w.Code)
		}
	}
}

func TestDispatchNotifRoutes_CountTracksReadAndDismiss(t *testing.T) {
	f := newDispatchNotifFixture(t)
	a := f.insert(t, f.callerID, "a", false)
	b := f.insert(t, f.callerID, "b", false)
	f.insert(t, f.callerID, "c", false)
	f.insert(t, f.callerID, "d", false)
	f.insert(t, f.otherID, "other", false)
	f.assertUnread(t, 4)

	for range 2 {
		w := f.serve(f.e.dispatchNotifReadRoute, f.callerID, http.MethodPost, "/_notif/"+a+"/read", map[string]string{"id": a})
		if w.Code != http.StatusNoContent {
			t.Fatalf("POST read status = %d, want 204; body: %s", w.Code, w.Body.String())
		}
	}
	f.assertUnread(t, 3)

	w := f.serve(f.e.dispatchNotifDismissRoute, f.callerID, http.MethodDelete, "/_notif/"+b, map[string]string{"id": b})
	if w.Code != http.StatusNoContent {
		t.Fatalf("DELETE status = %d, want 204; body: %s", w.Code, w.Body.String())
	}
	f.assertUnread(t, 2)
	if got := titles(f.feed(t, f.callerID, nil).Data); fmt.Sprint(got) != "[d c a]" {
		t.Errorf("feed after dismiss = %v, want [d c a]", got)
	}

	w = f.serve(f.e.dispatchNotifReadAllRoute, f.callerID, http.MethodPost, "/_notif/read-all", nil)
	if w.Code != http.StatusNoContent {
		t.Fatalf("POST read-all status = %d, want 204", w.Code)
	}
	f.assertUnread(t, 0)
	if got := len(f.feed(t, f.callerID, nil).Data); got != 3 {
		t.Errorf("feed after read-all has %d items, want 3", got)
	}

	w = f.serve(f.e.dispatchNotifDismissAllRoute, f.callerID, http.MethodDelete, "/_notif/all", nil)
	if w.Code != http.StatusNoContent {
		t.Fatalf("DELETE all status = %d, want 204", w.Code)
	}
	if got := len(f.feed(t, f.callerID, nil).Data); got != 0 {
		t.Errorf("feed after dismiss-all has %d items, want 0", got)
	}
	f.assertUnread(t, 0)

	// The other user's notification is untouched by all of it.
	other := f.feed(t, f.otherID, nil)
	if len(other.Data) != 1 || other.Data[0].ReadAt != nil || other.Meta.Unread != 1 {
		t.Errorf("other user's feed = %+v, want one unread notification", other)
	}
}

func TestDispatchNotifRoutes_AnotherUsersNotificationIsNotFound(t *testing.T) {
	f := newDispatchNotifFixture(t)
	theirs := f.insert(t, f.otherID, "theirs", false)

	for _, id := range []string{theirs, uuid.New().String(), "not-a-uuid"} {
		params := map[string]string{"id": id}
		if w := f.serve(f.e.dispatchNotifReadRoute, f.callerID, http.MethodPost, "/_notif/"+id+"/read", params); w.Code != http.StatusNotFound {
			t.Errorf("POST read %s: status = %d, want 404", id, w.Code)
		}
		if w := f.serve(f.e.dispatchNotifDismissRoute, f.callerID, http.MethodDelete, "/_notif/"+id, params); w.Code != http.StatusNotFound {
			t.Errorf("DELETE %s: status = %d, want 404", id, w.Code)
		}
	}

	other := f.feed(t, f.otherID, nil)
	if len(other.Data) != 1 || other.Data[0].ReadAt != nil {
		t.Errorf("other user's notification changed: %+v", other.Data)
	}
}

func (f *dispatchNotifFixture) registerToken(t *testing.T, callerID string, fields map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	body, _ := json.Marshal(fields)
	return f.serveBody(f.e.dispatchNotifDeviceTokenRoute, callerID, http.MethodPost, "/_notif/device-token", body, nil)
}

type deviceTokenRow struct {
	userID, platform, token string
	appVersion              *string
	registeredAt, lastSeen  time.Time
}

func (f *dispatchNotifFixture) deviceTokens(t *testing.T) []deviceTokenRow {
	t.Helper()
	rows, err := f.e.primaryDB.QueryContext(t.Context(), fmt.Sprintf(
		`SELECT user_id, platform, token, app_version, registered_at, last_seen_at FROM %s.user_device_tokens WHERE tenant_id = $1 ORDER BY id`,
		tenantschema.Name(f.slug)), f.tenantID)
	if err != nil {
		t.Fatalf("select device tokens: %v", err)
	}
	defer rows.Close()
	var out []deviceTokenRow
	for rows.Next() {
		var r deviceTokenRow
		if err := rows.Scan(&r.userID, &r.platform, &r.token, &r.appVersion, &r.registeredAt, &r.lastSeen); err != nil {
			t.Fatalf("scan device token: %v", err)
		}
		out = append(out, r)
	}
	return out
}

func TestDispatchNotifDeviceTokenRoute_UpsertsTheCallersToken(t *testing.T) {
	f := newDispatchNotifFixture(t)

	w := f.registerToken(t, f.callerID, map[string]any{"platform": "android", "token": "fcm-token", "app_version": "1.2.0"})
	if w.Code != http.StatusNoContent {
		t.Fatalf("first register status = %d, want 204; body: %s", w.Code, w.Body.String())
	}
	first := f.deviceTokens(t)
	if len(first) != 1 || first[0].userID != f.callerID || first[0].platform != "android" || first[0].token != "fcm-token" ||
		first[0].appVersion == nil || *first[0].appVersion != "1.2.0" {
		t.Fatalf("tokens after first register = %+v", first)
	}

	w = f.registerToken(t, f.callerID, map[string]any{"platform": "android", "token": "fcm-token", "app_version": "1.3.0"})
	if w.Code != http.StatusNoContent {
		t.Fatalf("repeat register status = %d, want 204; body: %s", w.Code, w.Body.String())
	}
	again := f.deviceTokens(t)
	if len(again) != 1 {
		t.Fatalf("tokens after repeat register = %+v, want one row", again)
	}
	if !again[0].lastSeen.After(first[0].lastSeen) {
		t.Errorf("last_seen_at = %v, want later than %v", again[0].lastSeen, first[0].lastSeen)
	}
	if !again[0].registeredAt.Equal(first[0].registeredAt) {
		t.Errorf("registered_at changed from %v to %v", first[0].registeredAt, again[0].registeredAt)
	}
	if again[0].appVersion == nil || *again[0].appVersion != "1.3.0" {
		t.Errorf("app_version = %v, want 1.3.0", again[0].appVersion)
	}

	// A different token is its own row.
	if w := f.registerToken(t, f.callerID, map[string]any{"platform": "web", "token": "web-token"}); w.Code != http.StatusNoContent {
		t.Fatalf("second token register status = %d, want 204; body: %s", w.Code, w.Body.String())
	}
	if all := f.deviceTokens(t); len(all) != 2 || all[1].token != "web-token" || all[1].appVersion != nil {
		t.Errorf("tokens = %+v, want a second row for web-token with no app_version", all)
	}
}

func TestDispatchNotifDeviceTokenRoute_MovesASharedDevicesTokenToItsNewUser(t *testing.T) {
	f := newDispatchNotifFixture(t)
	for _, userID := range []string{f.callerID, f.otherID} {
		if w := f.registerToken(t, userID, map[string]any{"platform": "ios", "token": "shared-device"}); w.Code != http.StatusNoContent {
			t.Fatalf("register status = %d, want 204; body: %s", w.Code, w.Body.String())
		}
	}
	all := f.deviceTokens(t)
	if len(all) != 1 || all[0].userID != f.otherID {
		t.Fatalf("tokens = %+v, want only the latest user's row", all)
	}

	// The previous user's other devices keep their tokens.
	if w := f.registerToken(t, f.callerID, map[string]any{"platform": "android", "token": "own-device"}); w.Code != http.StatusNoContent {
		t.Fatalf("register status = %d, want 204; body: %s", w.Code, w.Body.String())
	}
	if w := f.registerToken(t, f.otherID, map[string]any{"platform": "ios", "token": "shared-device"}); w.Code != http.StatusNoContent {
		t.Fatalf("register status = %d, want 204; body: %s", w.Code, w.Body.String())
	}
	if got := f.deviceTokens(t); len(got) != 2 {
		t.Errorf("tokens = %+v, want the shared device's row and the previous user's own device", got)
	}
}

func TestDispatchNotifDeviceTokenRoute_RejectsAnInvalidPlatformOrToken(t *testing.T) {
	f := newDispatchNotifFixture(t)
	for _, fields := range []map[string]any{
		{"platform": "blackberry", "token": "t"},
		{"platform": "", "token": "t"},
		{"token": "t"},
		{"platform": "ios", "token": ""},
		{"platform": "ios"},
		{"platform": "ios", "token": strings.Repeat("x", 4097)},
	} {
		w := f.registerToken(t, f.callerID, fields)
		if w.Code != http.StatusBadRequest {
			t.Errorf("%v: status = %d, want 400", fields, w.Code)
			continue
		}
		var body struct {
			Error struct {
				Code string `json:"code"`
			} `json:"error"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil || body.Error.Code != "invalid_request" {
			t.Errorf("%v: body = %s, want error code invalid_request", fields, w.Body.String())
		}
	}
	if got := f.deviceTokens(t); len(got) != 0 {
		t.Errorf("tokens = %+v, want none", got)
	}
}

// withRealTenant replaces the fixture's random tenant id with a real
// system.tenants row, which tenant_module_settings rows need.
func (f *dispatchNotifFixture) withRealTenant(t *testing.T) {
	t.Helper()
	ctx := t.Context()
	tenantStore := tenant.NewStore(f.e.primaryDB)
	if err := tenantStore.Bootstrap(ctx); err != nil {
		t.Fatalf("tenant Bootstrap() error: %v", err)
	}
	if err := billing.NewStore(f.e.primaryDB).Bootstrap(ctx); err != nil {
		t.Fatalf("billing Bootstrap() error: %v", err)
	}
	tt, err := tenantStore.CreateTenant(ctx, f.slug, "Notification Preferences Test")
	if err != nil {
		t.Fatalf("CreateTenant() error: %v", err)
	}
	t.Cleanup(func() { _, _ = f.e.primaryDB.Exec("DELETE FROM system.tenants WHERE id = $1", tt.ID) })
	f.tenantID = tt.ID
}

func (f *dispatchNotifFixture) setModule(t *testing.T, name, providerCategory string, enabled bool) {
	t.Helper()
	_, err := f.e.primaryDB.ExecContext(t.Context(), `
		INSERT INTO system.tenant_module_settings (tenant_id, module_name, enabled, provider_category)
		VALUES ($1, $2, $3, NULLIF($4, ''))
		ON CONFLICT (tenant_id, module_name) DO UPDATE SET enabled = EXCLUDED.enabled, provider_category = EXCLUDED.provider_category
	`, f.tenantID, name, enabled, providerCategory)
	if err != nil {
		t.Fatalf("set tenant_module_settings row: %v", err)
	}
}

func (f *dispatchNotifFixture) getPreferences(t *testing.T) notifPreferencesResponse {
	t.Helper()
	w := f.serve(f.e.dispatchNotifPreferencesRoute, f.callerID, http.MethodGet, "/_notif/preferences", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("GET /_notif/preferences status = %d, want 200; body: %s", w.Code, w.Body.String())
	}
	var resp notifPreferencesResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode preferences: %v", err)
	}
	return resp
}

func (f *dispatchNotifFixture) patchPreferences(t *testing.T, body string) *httptest.ResponseRecorder {
	t.Helper()
	return f.serveBody(f.e.dispatchNotifPreferencesUpdateRoute, f.callerID, http.MethodPatch, "/_notif/preferences", []byte(body), nil)
}

func TestDispatchNotifPreferencesRoute_DefaultsForAUserWithNoRows(t *testing.T) {
	f := newDispatchNotifFixture(t)
	f.withRealTenant(t)

	got := f.getPreferences(t)
	if fmt.Sprint(got.AvailableChannels) != "[in_app email]" {
		t.Errorf("available_channels = %v, want [in_app email]", got.AvailableChannels)
	}
	if got.Global != (notifications.Channels{Email: true, SMS: false, Push: true}) {
		t.Errorf("global = %+v, want email and push on, sms off", got.Global)
	}
	if got.Types == nil || len(got.Types) != 0 {
		t.Errorf("types = %#v, want an empty object", got.Types)
	}
}

func TestDispatchNotifPreferencesRoute_PatchThenGetReturnsTheSavedValues(t *testing.T) {
	f := newDispatchNotifFixture(t)
	f.withRealTenant(t)
	f.setModule(t, "sms_connector", "sms_provider", true)
	f.setModule(t, "push_connector", "push_provider", true)

	w := f.patchPreferences(t, `{
		"global": {"email": false, "sms": true},
		"types": {
			"sales.order_confirmed": {"push": false},
			"accounting.invoice_overdue": {"email": false, "sms": true, "push": true}
		}
	}`)
	if w.Code != http.StatusOK {
		t.Fatalf("PATCH status = %d, want 200; body: %s", w.Code, w.Body.String())
	}

	got := f.getPreferences(t)
	wantGlobal := notifications.Channels{Email: false, SMS: true, Push: true}
	if got.Global != wantGlobal {
		t.Errorf("global = %+v, want %+v", got.Global, wantGlobal)
	}
	// A new type row takes unset channels from global; one equal to global
	// isn't listed.
	wantTypes := map[string]notifications.Channels{"sales.order_confirmed": {Email: false, SMS: true, Push: false}}
	if fmt.Sprint(got.Types) != fmt.Sprint(wantTypes) {
		t.Errorf("types = %+v, want %+v", got.Types, wantTypes)
	}
	var patched notifPreferencesResponse
	if err := json.Unmarshal(w.Body.Bytes(), &patched); err != nil || patched.Global != wantGlobal {
		t.Errorf("PATCH response = %s, want the updated preferences", w.Body.String())
	}

	// A later partial patch changes only what it names.
	if w := f.patchPreferences(t, `{"types": {"sales.order_confirmed": {"email": true}}}`); w.Code != http.StatusOK {
		t.Fatalf("second PATCH status = %d; body: %s", w.Code, w.Body.String())
	}
	got = f.getPreferences(t)
	if c := got.Types["sales.order_confirmed"]; c != (notifications.Channels{Email: true, SMS: true, Push: false}) {
		t.Errorf("sales.order_confirmed = %+v, want email and sms on, push off", c)
	}
	if got.Global != wantGlobal {
		t.Errorf("global = %+v after a types-only patch, want %+v", got.Global, wantGlobal)
	}
}

func TestDispatchNotifPreferencesRoute_IgnoresUnavailableChannels(t *testing.T) {
	f := newDispatchNotifFixture(t)
	f.withRealTenant(t)

	w := f.patchPreferences(t, `{"global": {"email": false, "sms": true, "push": false, "in_app": false}, "types": {"hr.leave_approved": {"sms": true}}}`)
	if w.Code != http.StatusOK {
		t.Fatalf("PATCH status = %d, want 200; body: %s", w.Code, w.Body.String())
	}
	got := f.getPreferences(t)
	if got.Global != (notifications.Channels{Email: false, SMS: false, Push: true}) {
		t.Errorf("global = %+v, want only email changed", got.Global)
	}
	if len(got.Types) != 0 {
		t.Errorf("types = %+v, want none: the only change was to an unavailable channel", got.Types)
	}
}

func TestDispatchNotifPreferencesRoute_AvailableChannelsFollowEnabledProviderModules(t *testing.T) {
	f := newDispatchNotifFixture(t)
	f.withRealTenant(t)

	f.setModule(t, "crm", "", true)
	f.setModule(t, "sms_connector", "sms_provider", false)
	if got := f.getPreferences(t).AvailableChannels; fmt.Sprint(got) != "[in_app email]" {
		t.Errorf("with a disabled SMS provider: available_channels = %v, want [in_app email]", got)
	}

	f.setModule(t, "sms_connector", "sms_provider", true)
	if got := f.getPreferences(t).AvailableChannels; fmt.Sprint(got) != "[in_app email sms]" {
		t.Errorf("with an enabled SMS provider: available_channels = %v, want [in_app email sms]", got)
	}

	f.setModule(t, "push_connector", "push_provider", true)
	f.setModule(t, "sms_connector", "sms_provider", false)
	if got := f.getPreferences(t).AvailableChannels; fmt.Sprint(got) != "[in_app email push]" {
		t.Errorf("with only a push provider enabled: available_channels = %v, want [in_app email push]", got)
	}
}

func TestDispatchNotifPreferencesRoute_PatchInvalidatesTheCache(t *testing.T) {
	f := newDispatchNotifFixture(t)
	f.withRealTenant(t)
	f.e.notificationStore.WithCache(newRateLimitTestCacheClient(t))

	if got := f.getPreferences(t).Global; !got.Email {
		t.Fatalf("global = %+v, want the email default on", got)
	}

	// The cache now holds the defaults: a direct write isn't seen...
	_, err := f.e.primaryDB.ExecContext(t.Context(), fmt.Sprintf(
		`INSERT INTO %s.notification_preferences (tenant_id, user_id, push_enabled) VALUES ($1, $2, false)`,
		tenantschema.Name(f.slug)), f.tenantID, f.callerID)
	if err != nil {
		t.Fatalf("insert preference row: %v", err)
	}
	if got := f.getPreferences(t).Global; !got.Push {
		t.Fatalf("global = %+v, want the cached push default still served", got)
	}

	// ...but a PATCH drops the cached entry, so the next GET reads both it
	// and the direct write.
	if w := f.patchPreferences(t, `{"global": {"email": false}}`); w.Code != http.StatusOK {
		t.Fatalf("PATCH status = %d; body: %s", w.Code, w.Body.String())
	}
	if got := f.getPreferences(t).Global; got != (notifications.Channels{Email: false, SMS: false, Push: false}) {
		t.Errorf("global after PATCH = %+v, want email and push off", got)
	}
}

func TestDispatchNotifPreferencesUpdateRoute_RejectsABadBody(t *testing.T) {
	f := newDispatchNotifFixture(t)
	f.withRealTenant(t)
	for _, body := range []string{`nope`, `[]`, `{"global": {"email": "yes"}}`, `{"types": {"": {"email": true}}}`, `{"types": {"` + strings.Repeat("x", 201) + `": {}}}`} {
		if w := f.patchPreferences(t, body); w.Code != http.StatusBadRequest {
			t.Errorf("%s: status = %d, want 400", body, w.Code)
		}
	}
}
