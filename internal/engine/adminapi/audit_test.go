package adminapi

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/auditlog"
	"github.com/djangbahevans/goerp/internal/engine/db"
)

// localPostgresDSN is declared once for the whole package in tenant_test.go.

func openTestAuditStore(t *testing.T) (*auditlog.Store, *sql.DB) {
	t.Helper()

	conn, err := db.New(localPostgresDSN)
	if err != nil {
		t.Skipf("postgres not reachable at %s (start compose.dev.yml): %v", localPostgresDSN, err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	store := auditlog.NewStore(conn)
	if err := store.Bootstrap(context.Background()); err != nil {
		t.Fatalf("Bootstrap() error: %v", err)
	}

	return store, conn
}

func endpointToken(t *testing.T) string {
	t.Helper()
	return fmt.Sprintf("/admin/_audit_test_%d", time.Now().UnixNano())
}

func latestAuditRow(t *testing.T, conn *sql.DB, endpoint string) (operatorIdentity, targetScope, idempotencyKey, jobID string, statusCode int, found bool) {
	t.Helper()
	err := conn.QueryRowContext(context.Background(), `
		SELECT operator_identity, target_scope, COALESCE(idempotency_key, ''), COALESCE(job_id, ''), status_code
		FROM system.admin_audit_log
		WHERE endpoint = $1
		ORDER BY created_at DESC
		LIMIT 1
	`, endpoint).Scan(&operatorIdentity, &targetScope, &idempotencyKey, &jobID, &statusCode)
	if err == sql.ErrNoRows {
		return "", "", "", "", 0, false
	}
	if err != nil {
		t.Fatalf("query latest audit row for %q: %v", endpoint, err)
	}
	return operatorIdentity, targetScope, idempotencyKey, jobID, statusCode, true
}

func TestAuditLogMiddleware_GETIsNotAudited(t *testing.T) {
	store, conn := openTestAuditStore(t)
	endpoint := endpointToken(t)
	path := endpoint

	h := auditLogMiddleware(store)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	mux := http.NewServeMux()
	mux.Handle("GET "+path, h)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))

	if _, _, _, _, _, found := latestAuditRow(t, conn, "GET "+path); found {
		t.Error("expected no audit row for a GET request")
	}
}

func TestAuditLogMiddleware_MutatingRequestWritesRow(t *testing.T) {
	store, conn := openTestAuditStore(t)
	endpoint := endpointToken(t)
	path := endpoint

	h := auditLogMiddleware(store)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"data":{"status":"ok"},"error":null}`))
	}))
	mux := http.NewServeMux()
	mux.Handle("POST "+path+"/{slug}/suspend", h)

	req := httptest.NewRequest(http.MethodPost, path+"/acme/suspend", strings.NewReader(`{"reason":"test"}`))
	req.Header.Set("Idempotency-Key", "idem-xyz")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	wantEndpoint := "POST " + path + "/{slug}/suspend"
	identity, scope, idemKey, jobID, status, found := latestAuditRow(t, conn, wantEndpoint)
	if !found {
		t.Fatal("expected an audit row for the mutating request")
	}
	if identity != "internal" {
		t.Errorf("operator_identity = %q, want %q (no forwarded identity header sent)", identity, "internal")
	}
	if scope != "acme" {
		t.Errorf("target_scope = %q, want %q", scope, "acme")
	}
	if idemKey != "idem-xyz" {
		t.Errorf("idempotency_key = %q, want %q", idemKey, "idem-xyz")
	}
	if jobID != "" {
		t.Errorf("job_id = %q, want empty for a 200 response", jobID)
	}
	if status != http.StatusOK {
		t.Errorf("status_code = %d, want %d", status, http.StatusOK)
	}
}

func TestAuditLogMiddleware_ForwardedIdentityHeaderIsUsed(t *testing.T) {
	store, conn := openTestAuditStore(t)
	endpoint := endpointToken(t)
	path := endpoint

	h := auditLogMiddleware(store)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	mux := http.NewServeMux()
	mux.Handle("POST "+path, h)

	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{}`))
	req.Header.Set(operatorIdentityHeader, "operator-cn-jane")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	identity, _, _, _, _, found := latestAuditRow(t, conn, "POST "+path)
	if !found {
		t.Fatal("expected an audit row for the mutating request")
	}
	if identity != "operator-cn-jane" {
		t.Errorf("operator_identity = %q, want %q", identity, "operator-cn-jane")
	}
}

func TestAuditLogMiddleware_CreateRouteScopeFromBody(t *testing.T) {
	store, conn := openTestAuditStore(t)
	endpoint := endpointToken(t)
	path := endpoint

	h := auditLogMiddleware(store)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"data":{"slug":"acme","job_id":"job_123"},"error":null}`))
	}))
	mux := http.NewServeMux()
	mux.Handle("POST "+path, h)

	// bodyCapMiddleware normally stashes the buffered body in context ahead
	// of auditLogMiddleware; wire it in here too since this test exercises
	// auditLogMiddleware directly rather than through the full server chain.
	full := bodyCapMiddleware(1 << 20)(mux)

	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{"slug":"acme","name":"Acme"}`))
	rec := httptest.NewRecorder()
	full.ServeHTTP(rec, req)

	_, scope, _, jobID, status, found := latestAuditRow(t, conn, "POST "+path)
	if !found {
		t.Fatal("expected an audit row for the create request")
	}
	if scope != "acme" {
		t.Errorf("target_scope = %q, want %q (from request body, no path slug)", scope, "acme")
	}
	if jobID != "job_123" {
		t.Errorf("job_id = %q, want %q", jobID, "job_123")
	}
	if status != http.StatusAccepted {
		t.Errorf("status_code = %d, want %d", status, http.StatusAccepted)
	}
}

func TestAuditLogMiddleware_HandlerErrorStillWritesRow(t *testing.T) {
	store, conn := openTestAuditStore(t)
	endpoint := endpointToken(t)
	path := endpoint

	h := auditLogMiddleware(store)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeError(w, http.StatusInternalServerError, "internal", "boom")
	}))
	mux := http.NewServeMux()
	mux.Handle("POST "+path, h)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{}`)))

	_, _, _, _, status, found := latestAuditRow(t, conn, "POST "+path)
	if !found {
		t.Fatal("expected an audit row even when the handler returns an error status")
	}
	if status != http.StatusInternalServerError {
		t.Errorf("status_code = %d, want %d", status, http.StatusInternalServerError)
	}
}
