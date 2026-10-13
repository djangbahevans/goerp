package adminapi

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"uuid"

	"github.com/djangbahevans/goerp/internal/engine/cache"
)

func openIdempotencyStore(t *testing.T) *cache.Client {
	t.Helper()

	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()

	store, err := cache.New(ctx, cache.Config{Addr: "localhost:6379", DB: 1, MaxRetries: -1})
	if err != nil {
		t.Skipf("dev Redis unavailable: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	return store
}

func keyedRequest(method, path, key string) *http.Request {
	r := httptest.NewRequest(method, path, strings.NewReader(`{"reason":"test"}`))
	r.Header.Set("Idempotency-Key", key)
	return r
}

func TestAdminIdempotencyReplay(t *testing.T) {
	store := openIdempotencyStore(t)
	for _, status := range []int{http.StatusOK, http.StatusAccepted, http.StatusNoContent, http.StatusBadRequest, http.StatusInternalServerError} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			key := uuid.New().String()
			calls := 0
			h := idempotencyMiddleware(store)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				r.Pattern = "POST /admin/tenants/{slug}/export"
				r.SetPathValue("slug", "acme")
				w.Header().Set("X-Response", "original")
				w.WriteHeader(status)
				if status != http.StatusNoContent {
					_, _ = w.Write([]byte(`{"data":{"job_id":"job_123"},"error":null}`))
				}
			}))
			first := httptest.NewRecorder()
			h.ServeHTTP(first, keyedRequest(http.MethodPost, "/admin/tenants/acme/export", key))
			value, ttl, found, err := store.GetWithTTL(t.Context(), adminIdempotencyKey(keyedRequest(http.MethodPost, "/admin/tenants/acme/export", key)))
			if err != nil || !found || ttl <= idempotencyTTL-time.Minute || ttl > idempotencyTTL {
				t.Fatalf("response TTL=%v found=%t err=%v", ttl, found, err)
			}
			if err := store.SetWithTTL(t.Context(), adminIdempotencyKey(keyedRequest(http.MethodPost, "/admin/tenants/acme/export", key)), value, time.Minute); err != nil {
				t.Fatal(err)
			}

			req := keyedRequest(http.MethodPost, "/admin/tenants/acme/export", key)
			replay := httptest.NewRecorder()
			h.ServeHTTP(replay, req)
			if first.Code != status || replay.Code != status || first.Body.String() != replay.Body.String() || replay.Header().Get("X-Response") != "original" || calls != 1 {
				t.Fatalf("first=%d %s replay=%d %s calls=%d", first.Code, first.Body.String(), replay.Code, replay.Body.String(), calls)
			}
			if req.Pattern != "POST /admin/tenants/{slug}/export" || req.PathValue("slug") != "acme" {
				t.Fatalf("replayed audit route: %q %q", req.Pattern, req.PathValue("slug"))
			}
			_, ttl, found, err = store.GetWithTTL(t.Context(), adminIdempotencyKey(req))
			if err != nil || !found || ttl <= 0 || ttl > time.Minute {
				t.Fatalf("response TTL=%v found=%t err=%v", ttl, found, err)
			}
		})
	}
}

func TestAdminIdempotencyScopeAndBypass(t *testing.T) {
	store := openIdempotencyStore(t)
	key := uuid.New().String()
	calls := 0
	h := idempotencyMiddleware(store)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++

		writeData(w, http.StatusOK, calls)
	}))
	for _, tc := range []struct{ method, path, key string }{
		{http.MethodPost, "/admin/tenants/one/export", key},
		{http.MethodPost, "/admin/tenants/two/export", key},
		{http.MethodPatch, "/admin/tenants/two/export", key},
		{http.MethodDelete, "/admin/tenants/two/export", key},
		{http.MethodPost, "/admin/tenants/two/export?dry_run=true", key},
		{http.MethodPost, "/admin/tenants/two/export", key + "other"},
		{http.MethodHead, "/admin/tenants/two/export", key},
		{http.MethodOptions, "/admin/tenants/two/export", key},
		{http.MethodGet, "/admin/tenants/two/export", key},
		{http.MethodGet, "/admin/tenants/two/export", key},
		{http.MethodPost, "/admin/tenants/two/export", ""},
		{http.MethodPost, "/admin/tenants/two/export", ""},
	} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, keyedRequest(tc.method, tc.path, tc.key))
		if rec.Code != http.StatusOK {
			t.Fatalf("%s %s: %d", tc.method, tc.path, rec.Code)
		}
	}
	if calls != 12 {
		t.Fatalf("calls=%d want 12", calls)
	}
}

func TestAdminIdempotencyExpiration(t *testing.T) {
	store := openIdempotencyStore(t)
	req := keyedRequest(http.MethodPost, "/admin/schema/sync", uuid.New().String())
	key := adminIdempotencyKey(req)
	if err := store.SetWithTTL(t.Context(), key, `{"status":202,"body":"e30="}`, 10*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		_, found, err := store.Get(t.Context(), key)
		if err != nil {
			t.Fatal(err)
		}
		if !found {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("key did not expire")
		}
		time.Sleep(time.Millisecond)
	}
	calls := 0
	h := idempotencyMiddleware(store)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++

		writeData(w, http.StatusAccepted, "new")
	}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted || calls != 1 || !strings.Contains(rec.Body.String(), "new") {
		t.Fatalf("expired replay=%d %s calls=%d", rec.Code, rec.Body.String(), calls)
	}
}

func TestAdminIdempotencyConcurrentAcrossServers(t *testing.T) {
	store := openIdempotencyStore(t)
	other := openIdempotencyStore(t)
	key := uuid.New().String()
	entered := make(chan struct{})
	release := make(chan struct{})
	var calls atomic.Int32
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			close(entered)
		}
		<-release
		writeData(w, http.StatusAccepted, map[string]string{"job_id": "job_original"})
	})
	h := idempotencyMiddleware(store)(handler)
	follower := idempotencyMiddleware(other)(handler)
	first := httptest.NewRecorder()
	var wg sync.WaitGroup
	wg.Go(func() { h.ServeHTTP(first, keyedRequest(http.MethodPost, "/admin/schema/sync", key)) })
	<-entered
	duplicate := httptest.NewRecorder()
	follower.ServeHTTP(duplicate, keyedRequest(http.MethodPost, "/admin/schema/sync", key))
	close(release)
	wg.Wait()
	if duplicate.Code != http.StatusConflict || duplicate.Header().Get("Retry-After") != "1" || !strings.Contains(duplicate.Body.String(), "idempotency_in_progress") {
		t.Fatalf("in-flight response=%d %s", duplicate.Code, duplicate.Body.String())
	}
	replay := httptest.NewRecorder()
	follower.ServeHTTP(replay, keyedRequest(http.MethodPost, "/admin/schema/sync", key))
	if calls.Load() != 1 || first.Code != http.StatusAccepted || replay.Body.String() != first.Body.String() {
		t.Fatalf("calls=%d first=%s replay=%s", calls.Load(), first.Body.String(), replay.Body.String())
	}
}

type failingIdempotencyStore struct {
	IdempotencyStore
	claimErr, readErr, saveErr error
}

func (s failingIdempotencyStore) SetNXWithTTL(ctx context.Context, key, value string, ttl time.Duration) (bool, error) {
	if s.claimErr != nil {
		return false, s.claimErr
	}
	return s.IdempotencyStore.SetNXWithTTL(ctx, key, value, ttl)
}
func (s failingIdempotencyStore) Get(ctx context.Context, key string) (string, bool, error) {
	if s.readErr != nil {
		return "", false, s.readErr
	}
	return s.IdempotencyStore.Get(ctx, key)
}
func (s failingIdempotencyStore) SetIfEqualWithTTL(ctx context.Context, key, expected, value string, ttl time.Duration) (bool, error) {
	if s.saveErr != nil {
		return false, s.saveErr
	}
	return s.IdempotencyStore.SetIfEqualWithTTL(ctx, key, expected, value, ttl)
}

func TestAdminIdempotencyStoreFailure(t *testing.T) {
	store := openIdempotencyStore(t)
	for _, stage := range []string{"claim", "read", "save"} {
		t.Run(stage, func(t *testing.T) {
			failed := errors.New("Redis unavailable")
			s := failingIdempotencyStore{IdempotencyStore: store}
			req := keyedRequest(http.MethodPost, "/admin/schema/sync", uuid.New().String())
			switch stage {
			case "claim":
				s.claimErr = failed
			case "read":
				s.readErr = failed
				if err := store.SetWithTTL(t.Context(), adminIdempotencyKey(req), "pending:other", idempotencyTTL); err != nil {
					t.Fatal(err)
				}
			case "save":
				s.saveErr = failed
			}
			calls := 0
			handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++

				writeData(w, http.StatusAccepted, "job")
			})
			rec := httptest.NewRecorder()
			idempotencyMiddleware(s)(handler).ServeHTTP(rec, req)
			if rec.Code != http.StatusServiceUnavailable {
				t.Fatalf("status=%d", rec.Code)
			}
			wantCalls := 0
			if stage == "save" {
				wantCalls = 1
			}
			if calls != wantCalls {
				t.Fatalf("calls=%d want %d", calls, wantCalls)
			}
			if stage == "save" {
				retry := httptest.NewRecorder()
				idempotencyMiddleware(store)(handler).ServeHTTP(retry, req)
				if retry.Code != http.StatusConflict || calls != 1 {
					t.Fatalf("save failure repeated mutation: status=%d calls=%d", retry.Code, calls)
				}
			}
		})
	}
}

func TestAdminIdempotencyDisconnectedClient(t *testing.T) {
	store := openIdempotencyStore(t)
	ctx, cancel := context.WithCancel(t.Context())
	req := keyedRequest(http.MethodPost, "/admin/schema/sync", uuid.New().String()).WithContext(ctx)
	calls := 0
	h := idempotencyMiddleware(store)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		cancel()
		writeData(w, http.StatusAccepted, "job")
	}))
	first := httptest.NewRecorder()
	h.ServeHTTP(first, req)
	replay := httptest.NewRecorder()
	h.ServeHTTP(replay, keyedRequest(req.Method, req.URL.String(), req.Header.Get("Idempotency-Key")))
	if first.Code != http.StatusAccepted || replay.Body.String() != first.Body.String() || calls != 1 {
		t.Fatalf("first=%d replay=%d calls=%d", first.Code, replay.Code, calls)
	}
}

func TestAdminIdempotencyAuditReplay(t *testing.T) {
	store := openIdempotencyStore(t)
	audit, conn := openTestAuditStore(t)
	srv, err := NewServer(&Config{AdminToken: "test", MaxBodyBytes: 1 << 20, MaxConcurrent: 4, AuditStore: audit, IdempotencyStore: store})
	if err != nil {
		t.Fatal(err)
	}
	path := endpointToken(t)
	calls := 0
	srv.Router().HandleFunc("POST "+path+"/{slug}/export", func(w http.ResponseWriter, r *http.Request) {
		calls++
		writeData(w, http.StatusAccepted, map[string]string{"job_id": "job_original"})
	})
	key := uuid.New().String()
	for range 2 {
		req := keyedRequest(http.MethodPost, path+"/acme/export", key)
		req.Header.Set("Authorization", "Bearer test")
		rec := httptest.NewRecorder()
		srv.http.Handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusAccepted {
			t.Fatalf("response=%d %s", rec.Code, rec.Body.String())
		}
	}
	var count int
	err = conn.QueryRowContext(t.Context(), `SELECT count(*) FROM system.admin_audit_log WHERE endpoint=$1 AND idempotency_key=$2 AND job_id='job_original' AND target_scope='acme' AND reason='test' AND status_code=202`, "POST "+path+"/{slug}/export", key).Scan(&count)
	if err != nil {
		t.Fatal(err)
	}
	if calls != 1 || count != 2 {
		t.Fatalf("handler calls=%d audited responses=%d", calls, count)
	}
}
