package adminapi

import (
	"encoding/json/v2"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/eventdelivery"
	"github.com/djangbahevans/goerp/internal/engine/jobqueue"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdbtest"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/riverqueue/river/rivertype"
	"github.com/vmihailenco/msgpack/v5"
)

type dlqAPIFixture struct {
	pool   *pgxpool.Pool
	client *river.Client[pgx.Tx]
	table  string
	mux    *http.ServeMux
}

func newDLQAPIFixture(t *testing.T) *dlqAPIFixture {
	t.Helper()
	pool, err := pgxpool.New(t.Context(), eventsLocalPostgresDSN)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err := pool.Ping(t.Context()); err != nil {
		t.Skipf("Postgres unavailable: %v", err)
	}
	driver := riverpgxv5.New(pool)
	schema := riverdbtest.TestSchema(t.Context(), t, driver, &riverdbtest.TestSchemaOpts{DisableReuse: true})
	client, err := river.NewClient(driver, &river.Config{Schema: schema})
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	RegisterEventsRoutes(mux, EventsDeps{DLQ: eventdelivery.NewDLQ(pool, schema)})
	return &dlqAPIFixture{
		pool: pool, client: client, table: pgx.Identifier{schema, "river_job"}.Sanitize(), mux: mux,
	}
}

func (f *dlqAPIFixture) insert(t *testing.T, tenantID, module, eventName string) int64 {
	t.Helper()
	payload, err := msgpack.Marshal(map[string]any{"order_id": "order-1"})
	if err != nil {
		t.Fatal(err)
	}
	result, err := f.client.Insert(t.Context(), jobqueue.SubscriberDeliveryArgs{
		EventID: "event-1", EventName: eventName, ModuleName: module, TenantID: tenantID,
		Payload: payload, HandlerName: "handle_order", UserID: "user-1", EventVersion: 2,
	}, &river.InsertOpts{MaxAttempts: 5, Tags: []string{jobqueue.ModuleVersionTag("1.0.0")}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(t.Context(), `UPDATE `+f.table+` SET state = 'discarded',
		attempt = 5, attempted_at = now(), finalized_at = now(),
		errors = ARRAY['{"attempt":5,"at":"2026-10-16T10:00:00Z","error":"handler failed"}'::jsonb]
		WHERE id = $1`, result.Job.ID); err != nil {
		t.Fatal(err)
	}
	return result.Job.ID
}

func (f *dlqAPIFixture) request(method, path, body string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	f.mux.ServeHTTP(w, httptest.NewRequest(method, path, strings.NewReader(body)))
	return w
}

func (f *dlqAPIFixture) list(t *testing.T, query string) eventdelivery.DLQPage {
	t.Helper()
	w := f.request(http.MethodGet, "/admin/events/dlq"+query, "")
	if w.Code != http.StatusOK {
		t.Fatalf("list status = %d: %s", w.Code, w.Body.String())
	}
	var response struct {
		Data eventdelivery.DLQPage `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	return response.Data
}

func TestEventsDLQList(t *testing.T) {
	f := newDLQAPIFixture(t)
	id := f.insert(t, "tenant-a", "inventory", "order.confirmed")
	f.insert(t, "tenant-a", "billing", "order.confirmed")
	f.insert(t, "tenant-b", "inventory", "order.confirmed")
	active := f.insert(t, "tenant-a", "inventory", "active.event")
	other := f.insert(t, "tenant-a", "inventory", "other.kind")
	if _, err := f.pool.Exec(t.Context(), `UPDATE `+f.table+` SET state='available', finalized_at=NULL WHERE id=$1`, active); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(t.Context(), `UPDATE `+f.table+` SET kind='probe' WHERE id=$1`, other); err != nil {
		t.Fatal(err)
	}

	page := f.list(t, "?tenant=tenant-a&module=inventory")
	if page.Total != 1 || len(page.Items) != 1 {
		t.Fatalf("filtered page = %+v", page)
	}
	item := page.Items[0]
	if item.ID != jobqueue.EncodeJobID(id) || item.EventID != "event-1" || item.EventName != "order.confirmed" ||
		item.SubscriberModule != "inventory" || item.TenantID != "tenant-a" || item.Attempts != 5 ||
		item.LastAttemptAt == nil || item.LastError != "handler failed" || item.CreatedAt.IsZero() {
		t.Fatalf("DLQ item = %+v", item)
	}
	preview, err := json.Marshal(item.PayloadPreview)
	if err != nil || string(preview) != `{"order_id":"order-1"}` {
		t.Fatalf("payload preview = %s, %v", preview, err)
	}

	for _, tc := range []struct {
		query string
		total int64
		items int
	}{
		{"", 3, 3},
		{"?tenant=tenant-a", 2, 2},
		{"?module=inventory", 2, 2},
		{"?tenant=tenant-a&module=billing", 1, 1},
		{"?tenant=tenant-a%27%20OR%20true--", 0, 0},
		{"?limit=1", 3, 1},
		{"?limit=1&offset=100", 3, 0},
	} {
		t.Run(tc.query, func(t *testing.T) {
			page := f.list(t, tc.query)
			if page.Total != tc.total || len(page.Items) != tc.items || page.Items == nil {
				t.Fatalf("page = %+v, want total %d and %d items", page, tc.total, tc.items)
			}
		})
	}

	first := f.list(t, "?limit=1")
	second := f.list(t, "?limit=1&offset=1")
	if first.Items[0].ID == second.Items[0].ID {
		t.Fatal("pagination repeated the same job")
	}
}

func TestEventsDLQRetryResetsBudgetAndPreservesEnvelope(t *testing.T) {
	f := newDLQAPIFixture(t)
	id := f.insert(t, "tenant-a", "inventory", "order.confirmed")
	before, err := f.client.JobGet(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	path := "/admin/events/dlq/" + jobqueue.EncodeJobID(id) + "/retry"
	w := f.request(http.MethodPost, path, "")
	if w.Code != http.StatusOK {
		t.Fatalf("retry status = %d: %s", w.Code, w.Body.String())
	}
	after, err := f.client.JobGet(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	if after.State != rivertype.JobStateAvailable || after.Queue != jobqueue.QueueDefault ||
		after.Attempt != 0 || after.MaxAttempts != 5 || after.FinalizedAt != nil ||
		after.AttemptedAt != nil || len(after.Errors) != 0 || after.ScheduledAt.After(time.Now()) {
		t.Fatalf("retried job = %+v", after)
	}
	if string(before.EncodedArgs) != string(after.EncodedArgs) || fmt.Sprint(before.Tags) != fmt.Sprint(after.Tags) ||
		string(before.Metadata) != string(after.Metadata) {
		t.Fatal("retry changed the event envelope, version tags or metadata")
	}
	if page := f.list(t, ""); page.Total != 0 {
		t.Fatalf("retry remains in DLQ: %+v", page)
	}
	if w := f.request(http.MethodPost, path, ""); w.Code != http.StatusNotFound {
		t.Fatalf("second retry status = %d", w.Code)
	}
}

func TestEventsDLQBulkRetryMatchesEveryFilter(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
		want int
	}{
		{"all filters", `{"tenant_id":"tenant-a","event_name":"order.confirmed","subscriber_module":"inventory"}`, 1},
		{"tenant", `{"tenant_id":"tenant-a"}`, 3},
		{"module", `{"subscriber_module":"inventory"}`, 3},
		{"event", `{"event_name":"order.confirmed"}`, 3},
		{"no match", `{"tenant_id":"missing"}`, 0},
		{"all", `{}`, 4},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newDLQAPIFixture(t)
			ids := []int64{
				f.insert(t, "tenant-a", "inventory", "order.confirmed"),
				f.insert(t, "tenant-b", "inventory", "order.confirmed"),
				f.insert(t, "tenant-a", "billing", "order.confirmed"),
				f.insert(t, "tenant-a", "inventory", "order.cancelled"),
			}
			w := f.request(http.MethodPost, "/admin/events/dlq/retry-all", tc.body)
			if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), fmt.Sprintf(`"retried":%d`, tc.want)) {
				t.Fatalf("bulk response = %d: %s", w.Code, w.Body.String())
			}
			for i, id := range ids {
				row, err := f.client.JobGet(t.Context(), id)
				if err != nil {
					t.Fatal(err)
				}
				if tc.name == "all filters" && (row.State == rivertype.JobStateAvailable) != (i == 0) {
					t.Fatalf("job %d state = %s", id, row.State)
				}
			}
			if page := f.list(t, ""); page.Total != int64(4-tc.want) {
				t.Fatalf("remaining DLQ total = %d", page.Total)
			}
		})
	}
}

func TestEventsDLQRetryRejectsOtherJobs(t *testing.T) {
	f := newDLQAPIFixture(t)
	for _, tc := range []struct {
		name   string
		update string
	}{
		{name: "other kind", update: "kind = 'probe'"},
		{name: "completed", update: "state = 'completed'"},
		{name: "cancelled", update: "state = 'cancelled'"},
		{name: "available", update: "state = 'available', finalized_at = NULL"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			id := f.insert(t, "tenant-a", "inventory", "order.confirmed")
			if _, err := f.pool.Exec(t.Context(), "UPDATE "+f.table+" SET "+tc.update+" WHERE id=$1", id); err != nil {
				t.Fatal(err)
			}
			w := f.request(http.MethodPost, "/admin/events/dlq/"+jobqueue.EncodeJobID(id)+"/retry", "")
			if w.Code != http.StatusNotFound {
				t.Fatalf("status = %d: %s", w.Code, w.Body.String())
			}
		})
	}
	w := f.request(http.MethodPost, "/admin/events/dlq/job_9223372036854775807/retry", "")
	if w.Code != http.StatusNotFound {
		t.Fatalf("missing job status = %d", w.Code)
	}
}

func TestEventsDLQConcurrentRetryClaimsOnce(t *testing.T) {
	f := newDLQAPIFixture(t)
	id := f.insert(t, "tenant-a", "inventory", "order.confirmed")
	results := make(chan int, 2)
	var wg sync.WaitGroup
	for range 2 {
		wg.Go(func() {
			results <- f.request(http.MethodPost, "/admin/events/dlq/"+jobqueue.EncodeJobID(id)+"/retry", "").Code
		})
	}
	wg.Wait()
	close(results)
	counts := map[int]int{}
	for code := range results {
		counts[code]++
	}
	if counts[http.StatusOK] != 1 || counts[http.StatusNotFound] != 1 {
		t.Fatalf("concurrent statuses = %v", counts)
	}
}

func TestEventsDLQInvalidRequests(t *testing.T) {
	mux := http.NewServeMux()
	RegisterEventsRoutes(mux, EventsDeps{})
	for _, tc := range []struct {
		method string
		path   string
		body   string
	}{
		{http.MethodGet, "/admin/events/dlq?limit=0", ""},
		{http.MethodGet, "/admin/events/dlq?limit=1001", ""},
		{http.MethodGet, "/admin/events/dlq?offset=-1", ""},
		{http.MethodGet, "/admin/events/dlq?limit=bad", ""},
		{http.MethodPost, "/admin/events/dlq/bad/retry", ""},
		{http.MethodPost, "/admin/events/dlq/job_0/retry", ""},
		{http.MethodPost, "/admin/events/dlq/retry-all", "{"},
		{http.MethodPost, "/admin/events/dlq/retry-all", "null"},
		{http.MethodPost, "/admin/events/dlq/retry-all", `{"tenant":"typo"}`},
	} {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body)))
		if w.Code != http.StatusBadRequest {
			t.Errorf("%s: status = %d", tc.path, w.Code)
		}
	}
}

func TestEventsDLQRequiresAdminAuthorization(t *testing.T) {
	server, err := NewServer(&Config{AdminToken: "admin-secret", MaxBodyBytes: 1 << 20, MaxConcurrent: 10})
	if err != nil {
		t.Fatal(err)
	}
	RegisterEventsRoutes(server.Router(), EventsDeps{})
	for _, authorization := range []string{"", "Bearer tenant-user-token"} {
		for _, route := range []struct{ method, path string }{
			{http.MethodGet, "/admin/events/dlq"},
			{http.MethodPost, "/admin/events/dlq/job_1/retry"},
			{http.MethodPost, "/admin/events/dlq/retry-all"},
		} {
			req := httptest.NewRequest(route.method, route.path, strings.NewReader("{}"))
			req.Header.Set("Authorization", authorization)
			w := httptest.NewRecorder()
			server.http.Handler.ServeHTTP(w, req)
			if w.Code != http.StatusUnauthorized {
				t.Errorf("%s with %q: status = %d", route.path, authorization, w.Code)
			}
		}
	}
}
