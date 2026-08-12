package adminapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/djangbahevans/goerp/internal/engine/db"
	"github.com/djangbahevans/goerp/internal/engine/schema"
	"github.com/djangbahevans/goerp/internal/engine/tenant"
)

const localPostgresDSN = "postgres://goerp:dev@localhost:55432/goerp"

func newTestTenantMux(t *testing.T) *http.ServeMux {
	t.Helper()

	conn, err := db.New(localPostgresDSN)
	if err != nil {
		t.Skipf("postgres not reachable at %s (start compose.dev.yml): %v", localPostgresDSN, err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	store := tenant.NewStore(conn)
	if err := store.Bootstrap(context.Background()); err != nil {
		t.Fatalf("Bootstrap() error: %v", err)
	}

	mux := http.NewServeMux()
	RegisterTenantRoutes(mux, TenantDeps{Store: store})

	return mux
}

func decodeEnvelope(t *testing.T, w *httptest.ResponseRecorder) envelope {
	t.Helper()
	var env envelope
	if err := json.NewDecoder(w.Body).Decode(&env); err != nil {
		t.Fatalf("decode envelope: %v (body: %s)", err, w.Body.String())
	}
	return env
}

func TestCreateRoute_NoProvisionerReturnsNotImplemented(t *testing.T) {
	mux := newTestTenantMux(t)

	req := httptest.NewRequest(http.MethodPost, "/admin/tenants", strings.NewReader(`{"slug":"acme","admin_email":"a@b.com"}`))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusNotImplemented {
		t.Errorf("status = %d, want %d", w.Code, http.StatusNotImplemented)
	}
	env := decodeEnvelope(t, w)
	if env.Error == nil || env.Error.Code != "not_implemented" {
		t.Errorf("error = %+v, want code %q", env.Error, "not_implemented")
	}
}

func TestResendInviteRoute_NoInviterReturnsNotImplemented(t *testing.T) {
	mux := newTestTenantMux(t)

	req := httptest.NewRequest(http.MethodPost, "/admin/tenants/acme/resend-invite", strings.NewReader(`{"email":"a@b.com"}`))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusNotImplemented {
		t.Errorf("status = %d, want %d", w.Code, http.StatusNotImplemented)
	}
}

func TestExportRoute_NoExporterReturnsNotImplemented(t *testing.T) {
	mux := newTestTenantMux(t)

	req := httptest.NewRequest(http.MethodPost, "/admin/tenants/acme/export", strings.NewReader(`{}`))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusNotImplemented {
		t.Errorf("status = %d, want %d", w.Code, http.StatusNotImplemented)
	}
}

func TestOffboardRoute_NoOffboarderReturnsNotImplemented(t *testing.T) {
	mux := newTestTenantMux(t)

	req := httptest.NewRequest(http.MethodPost, "/admin/tenants/acme/offboard", strings.NewReader(`{"confirm":"acme"}`))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusNotImplemented {
		t.Errorf("status = %d, want %d", w.Code, http.StatusNotImplemented)
	}
}

func TestListRoute_ReturnsEnvelopedTenants(t *testing.T) {
	mux := newTestTenantMux(t)

	req := httptest.NewRequest(http.MethodGet, "/admin/tenants", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d (body: %s)", w.Code, http.StatusOK, w.Body.String())
	}
	env := decodeEnvelope(t, w)
	if env.Error != nil {
		t.Errorf("unexpected error: %+v", env.Error)
	}
	if env.Data == nil {
		t.Error("expected non-nil data")
	}
}

func TestStatusRoute_UnknownSlugReturnsNotFound(t *testing.T) {
	mux := newTestTenantMux(t)

	req := httptest.NewRequest(http.MethodGet, "/admin/tenants/does-not-exist", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", w.Code, http.StatusNotFound)
	}
	env := decodeEnvelope(t, w)
	if env.Error == nil || env.Error.Code != "not_found" {
		t.Errorf("error = %+v, want code %q", env.Error, "not_found")
	}
}

func TestSuspendRoute_MissingReasonIsBadRequest(t *testing.T) {
	mux := newTestTenantMux(t)

	req := httptest.NewRequest(http.MethodPost, "/admin/tenants/acme/suspend", strings.NewReader(`{}`))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

// fakeSyncStatusReader lets TestStatusRoute_ReportsSyncRatio control the
// modules_synced/modules_total ratio without needing real
// module_schema_versions rows.
type fakeSyncStatusReader struct {
	statuses []schema.ModuleSyncStatus
}

func (f *fakeSyncStatusReader) StatusForTenant(ctx context.Context, tenantID string) ([]schema.ModuleSyncStatus, error) {
	return f.statuses, nil
}

func TestStatusRoute_ReportsSyncRatio(t *testing.T) {
	conn, err := db.New(localPostgresDSN)
	if err != nil {
		t.Skipf("postgres not reachable at %s (start compose.dev.yml): %v", localPostgresDSN, err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	store := tenant.NewStore(conn)
	if err := store.Bootstrap(context.Background()); err != nil {
		t.Fatalf("Bootstrap() error: %v", err)
	}
	created, err := store.CreateTenant(context.Background(), "statusratio1", "Status Ratio Test")
	if err != nil {
		t.Fatalf("CreateTenant() error: %v", err)
	}
	t.Cleanup(func() {
		_, _ = conn.Exec("DELETE FROM system.tenants WHERE id = $1", created.ID)
	})

	mux := http.NewServeMux()
	RegisterTenantRoutes(mux, TenantDeps{
		Store: store,
		SyncStatus: &fakeSyncStatusReader{statuses: []schema.ModuleSyncStatus{
			{ModuleName: "contacts", Status: "ok"},
			{ModuleName: "sales", Status: "ok"},
			{ModuleName: "hr", Status: "failed"},
		}},
	})

	req := httptest.NewRequest(http.MethodGet, "/admin/tenants/statusratio1", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d (body: %s)", w.Code, http.StatusOK, w.Body.String())
	}

	var env struct {
		Data struct {
			ModulesSynced int `json:"modules_synced"`
			ModulesTotal  int `json:"modules_total"`
		} `json:"data"`
	}
	if err := json.NewDecoder(w.Body).Decode(&env); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if env.Data.ModulesSynced != 2 {
		t.Errorf("ModulesSynced = %d, want 2", env.Data.ModulesSynced)
	}
	if env.Data.ModulesTotal != 3 {
		t.Errorf("ModulesTotal = %d, want 3", env.Data.ModulesTotal)
	}
}
