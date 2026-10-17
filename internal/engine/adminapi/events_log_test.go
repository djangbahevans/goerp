package adminapi

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"
	"uuid"

	"github.com/djangbahevans/goerp/internal/engine/auth/membership/membershiptest"
	"github.com/djangbahevans/goerp/internal/engine/enginetables"
	"github.com/djangbahevans/goerp/internal/engine/tenant"
	"github.com/djangbahevans/goerp/internal/engine/tenantschema"
	"github.com/vmihailenco/msgpack/v5"
)

type eventLogFixture struct {
	pool   *sql.DB
	server *Server
	slug   string
	other  string
	month  time.Time
}

func newEventLogFixture(t *testing.T) *eventLogFixture {
	t.Helper()
	pool := membershiptest.New(t)
	store := tenant.NewStore(pool)
	f := &eventLogFixture{
		pool: pool,
		slug: "log" + strings.ReplaceAll(uuid.NewV7().String(), "-", ""),
	}
	f.other = f.slug + "b"
	now := time.Now().UTC()
	f.month = time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	for _, slug := range []string{f.slug, f.other} {
		if _, err := store.CreateTenant(t.Context(), slug, "Event Log Test"); err != nil {
			t.Fatal(err)
		}
		if err := tenantschema.Create(t.Context(), pool, slug); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if err := tenantschema.Drop(context.Background(), pool, slug); err != nil {
				t.Errorf("drop event log tenant: %v", err)
			}
		})
		group := slices.IndexFunc(enginetables.Groups, func(g enginetables.Group) bool {
			return g.Tables[0].Name == "event_log"
		})
		if group < 0 {
			t.Fatal("event log table group missing")
		}
		if err := enginetables.Groups[group].Create(t.Context(), pool, slug); err != nil {
			t.Fatal(err)
		}
	}
	var database string
	if err := pool.QueryRowContext(t.Context(), "SELECT current_database()").Scan(&database); err != nil {
		t.Fatal(err)
	}
	primary := membershiptest.Open(t, database, "engine_user")
	server, err := NewServer(&Config{
		AdminToken:    "event-log-secret",
		MaxBodyBytes:  1 << 20,
		MaxConcurrent: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	f.server = server
	RegisterEventsRoutes(server.Router(), EventsDeps{
		Pool:        primary,
		TenantStore: tenant.NewStore(primary),
	})
	return f
}

func (f *eventLogFixture) insert(t *testing.T, slug, event, module string, at time.Time) string {
	t.Helper()
	id := uuid.NewV7().String()
	payload, err := msgpack.Marshal(map[string]any{
		"order_id": "order-1",
		"details":  map[string]any{"quantity": 3},
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.pool.ExecContext(t.Context(), `INSERT INTO `+tenantschema.Name(slug)+`.event_log
		(id, event_name, event_version, emitter_module, payload, trace_id, user_id, emitted_at)
		VALUES ($1, $2, 2, $3, $4, 'trace-1', $5, $6)`, id, event, module, payload, uuid.NewV7().String(), at)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func (f *eventLogFixture) query() url.Values {
	return url.Values{
		"tenant": {f.slug},
		"since":  {f.month.Format(time.RFC3339)},
		"until":  {f.month.AddDate(0, 1, 0).Format(time.RFC3339)},
	}
}

func (f *eventLogFixture) request(query url.Values, authorization string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, "/admin/events?"+query.Encode(), nil)
	req.Header.Set("Authorization", authorization)
	w := httptest.NewRecorder()
	f.server.http.Handler.ServeHTTP(w, req)
	return w
}

func (f *eventLogFixture) list(t *testing.T, query url.Values) eventLogPage {
	t.Helper()
	w := f.request(query, "Bearer event-log-secret")
	if w.Code != http.StatusOK {
		t.Fatalf("list status = %d: %s", w.Code, w.Body.String())
	}
	var response struct {
		Data eventLogPage `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	return response.Data
}

func TestEventLogFiltersAndPayload(t *testing.T) {
	f := newEventLogFixture(t)
	id := f.insert(t, f.slug, "order.confirmed", "sales", f.month)
	f.insert(t, f.slug, "order.cancelled", "sales", f.month.Add(time.Hour))
	f.insert(t, f.slug, "order.confirmed", "billing", f.month.Add(2*time.Hour))
	f.insert(t, f.slug, "order.confirmed", "sales", f.month.Add(-time.Microsecond))
	f.insert(t, f.slug, "order.confirmed", "sales", f.month.AddDate(0, 1, 0))
	f.insert(t, f.other, "order.confirmed", "sales", f.month)
	query := f.query()
	query.Set("event", "order.confirmed")
	query.Set("module", "sales")
	page := f.list(t, query)
	if page.Total != 1 || len(page.Events) != 1 || page.Cursor != "" {
		t.Fatalf("filtered page = %+v", page)
	}
	item := page.Events[0]
	if item.ID != id || item.Name != "order.confirmed" || item.Emitter != "sales" || item.Version != 2 ||
		item.TraceID == nil || *item.TraceID != "trace-1" || item.UserID == nil || !item.EmittedAt.Equal(f.month) {
		t.Fatalf("event = %+v", item)
	}
	encoded, err := json.Marshal(item.Payload, json.Deterministic(true))
	if err != nil || string(encoded) != `{"details":{"quantity":3},"order_id":"order-1"}` {
		t.Fatalf("decoded payload = %s, %v", encoded, err)
	}
	query.Del("module")
	if page := f.list(t, query); page.Total != 2 {
		t.Fatalf("event filter total = %d", page.Total)
	}
	query.Del("event")
	query.Set("module", "sales")
	if page := f.list(t, query); page.Total != 2 {
		t.Fatalf("module filter total = %d", page.Total)
	}
	query.Del("module")
	if page := f.list(t, query); page.Total != 3 || page.Events[0].Emitter != "billing" {
		t.Fatalf("unfiltered page = %+v", page)
	}
	query.Set("event", "missing' OR true--")
	if page := f.list(t, query); page.Total != 0 || page.Events == nil || len(page.Events) != 0 || page.Cursor != "" {
		t.Fatalf("empty page = %+v", page)
	}
	query.Set("tenant", "missing-tenant")
	if w := f.request(query, "Bearer event-log-secret"); w.Code != http.StatusNotFound {
		t.Fatalf("unknown tenant = %d: %s", w.Code, w.Body.String())
	}

	query = f.query()
	query.Set("until", f.month.Add(time.Nanosecond).Format(time.RFC3339Nano))
	if page := f.list(t, query); page.Total != 1 || page.Events[0].ID != id {
		t.Fatalf("submicrosecond upper bound = %+v", page)
	}
	query = f.query()
	query.Set("since", f.month.Add(time.Nanosecond).Format(time.RFC3339Nano))
	if page := f.list(t, query); page.Total != 2 {
		t.Fatalf("submicrosecond lower bound = %+v", page)
	}
}

func TestEventLogCursorAndNullMetadata(t *testing.T) {
	f := newEventLogFixture(t)
	at := f.month.Add(time.Hour)
	ids := []string{
		f.insert(t, f.slug, "order.confirmed", "sales", at),
		f.insert(t, f.slug, "order.confirmed", "sales", at),
		f.insert(t, f.slug, "order.confirmed", "sales", at.Add(-time.Second)),
		f.insert(t, f.slug, "order.confirmed", "sales", f.month.Add(-time.Hour)),
	}
	if _, err := f.pool.ExecContext(t.Context(), `UPDATE `+tenantschema.Name(f.slug)+`.event_log
		SET trace_id = NULL, user_id = NULL WHERE id = $1`, ids[2]); err != nil {
		t.Fatal(err)
	}
	slices.Sort(ids[:2])
	slices.Reverse(ids[:2])
	query := f.query()
	query.Set("since", f.month.AddDate(0, -1, 0).Format(time.RFC3339))
	query.Set("limit", "1")
	for i, id := range ids {
		page := f.list(t, query)
		if page.Total != int64(len(ids)) || len(page.Events) != 1 || page.Events[0].ID != id || (page.Cursor == "") != (i == len(ids)-1) {
			t.Fatalf("page %d = %+v", i, page)
		}
		if i == 2 && (page.Events[0].TraceID != nil || page.Events[0].UserID != nil) {
			t.Fatalf("nullable metadata = %+v", page.Events[0])
		}
		if i == 0 {
			changed := query.Clone()
			changed.Set("cursor", page.Cursor)
			changed.Set("tenant", f.other)
			if w := f.request(changed, "Bearer event-log-secret"); w.Code != http.StatusBadRequest {
				t.Fatalf("changed cursor tenant status = %d", w.Code)
			}
			f.insert(t, f.slug, "new.event", "sales", f.month.AddDate(0, 1, 0))
		}
		query.Set("cursor", page.Cursor)
	}
}

func TestEventLogInvalidStoredPayload(t *testing.T) {
	f := newEventLogFixture(t)
	id := f.insert(t, f.slug, "order.confirmed", "sales", f.month)
	if _, err := f.pool.ExecContext(t.Context(), `UPDATE `+tenantschema.Name(f.slug)+`.event_log
		SET payload = $1 WHERE id = $2`, []byte{0xc1}, id); err != nil {
		t.Fatal(err)
	}
	w := f.request(f.query(), "Bearer event-log-secret")
	if w.Code != http.StatusInternalServerError || !strings.Contains(w.Body.String(), `"data":null`) || !jsontext.Value(w.Body.Bytes()).IsValid() {
		t.Fatalf("invalid payload response = %d: %s", w.Code, w.Body.String())
	}
}

func TestEventLogPartitionPruning(t *testing.T) {
	f := newEventLogFixture(t)
	for _, continued := range []bool{false, true} {
		args := []any{f.month, f.month.AddDate(0, 1, 0), "", "", 51}
		if continued {
			args = append(args, f.month.Add(time.Hour), uuid.NewV7().String())
		}
		rows, err := f.pool.QueryContext(t.Context(), "EXPLAIN (FORMAT JSON) "+eventLogSelect(tenantschema.Name(f.slug)+".event_log", continued), args...)
		if err != nil {
			t.Fatal(err)
		}
		var plan []byte
		if !rows.Next() {
			t.Fatal("missing EXPLAIN plan")
		}
		if err := rows.Scan(&plan); err != nil {
			t.Fatal(err)
		}
		if err := rows.Close(); err != nil {
			t.Fatal(err)
		}
		var nodes []struct {
			Plan map[string]any `json:"Plan"`
		}
		if err := json.Unmarshal(plan, &nodes); err != nil {
			t.Fatal(err)
		}
		var relations []string
		var visit func(map[string]any)
		visit = func(node map[string]any) {
			if name, ok := node["Relation Name"].(string); ok {
				relations = append(relations, name)
			}
			if children, ok := node["Plans"].([]any); ok {
				for _, child := range children {
					visit(child.(map[string]any))
				}
			}
		}
		visit(nodes[0].Plan)
		if len(relations) != 1 || relations[0] != "event_log_p"+f.month.Format("20060102") {
			t.Fatalf("query scans %v: %s", relations, plan)
		}
	}
}

func TestEventLogQueryValidationAndFrozenWindow(t *testing.T) {
	now := time.Now().UTC()
	query := url.Values{"tenant": {"acmecorp"}}
	cursor, limit, err := parseEventLogQuery(query, now)
	if err != nil || limit != 50 || !cursor.From.Equal(now.Add(-time.Hour)) || !cursor.To.Equal(now) {
		t.Fatalf("default query = %+v, %d, %v", cursor, limit, err)
	}
	cursor.At, cursor.ID = now.Add(-time.Minute), uuid.NewV7().String()
	data, err := json.Marshal(cursor)
	if err != nil {
		t.Fatal(err)
	}
	query.Set("cursor", base64.RawURLEncoding.EncodeToString(data))
	continued, _, err := parseEventLogQuery(query, now.Add(time.Hour))
	if err != nil || continued != cursor {
		t.Fatalf("continued window = %+v, %v", continued, err)
	}
	for _, raw := range []string{
		"", "tenant=bad%27slug", "tenant=acmecorp&since=bad", "tenant=acmecorp&since=-1h",
		"tenant=acmecorp&since=0s", "tenant=acmecorp&until=bad", "tenant=acmecorp&limit=0",
		"tenant=acmecorp&limit=1001", "tenant=acmecorp&limit=bad", "tenant=acmecorp&cursor=garbage",
		"tenant=acmecorp&cursor=e30", "tenant=acmecorp&since=2026-11-01T00:00:00Z&until=2026-10-01T00:00:00Z",
		"tenant=acmecorp&failed=true", "tenant=acmecorp&tenant=other", "tenant=acmecorp&since=",
	} {
		mux := http.NewServeMux()
		RegisterEventsRoutes(mux, EventsDeps{})
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/admin/events?"+raw, nil))
		if w.Code != http.StatusBadRequest {
			t.Errorf("%s: status = %d", raw, w.Code)
		}
	}
}

func TestEventLogRequiresAdminAuthorization(t *testing.T) {
	server, err := NewServer(&Config{
		AdminToken:    "event-log-secret",
		MaxBodyBytes:  1 << 20,
		MaxConcurrent: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	RegisterEventsRoutes(server.Router(), EventsDeps{})
	for _, authorization := range []string{"", "Bearer tenant-user-token"} {
		req := httptest.NewRequest(http.MethodGet, "/admin/events?tenant=acmecorp", nil)
		req.Header.Set("Authorization", authorization)
		w := httptest.NewRecorder()
		server.http.Handler.ServeHTTP(w, req)
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("authorization %q: status = %d", authorization, w.Code)
		}
	}
}
