package engine

import (
	"encoding/json/v2"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"
	"uuid"

	"github.com/djangbahevans/goerp/internal/engine/auth/authcheck"
	"github.com/djangbahevans/goerp/internal/engine/notifications"
	"github.com/djangbahevans/goerp/internal/engine/route"
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
	r := httptest.NewRequest(method, target, nil)
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
