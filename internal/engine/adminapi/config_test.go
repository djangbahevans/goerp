package adminapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/db"
	"github.com/djangbahevans/goerp/internal/engine/tenant"
	"github.com/djangbahevans/goerp/internal/engine/tenantconfig"
)

type configTestEnv struct {
	mux         *http.ServeMux
	tenantStore *tenant.Store
	conn        *sql.DB
}

func newTestConfigMux(t *testing.T) *configTestEnv {
	t.Helper()
	ctx := context.Background()

	conn, err := db.New(localPostgresDSN)
	if err != nil {
		t.Skipf("postgres not reachable at %s (start compose.dev.yml): %v", localPostgresDSN, err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	tenantStore := tenant.NewStore(conn)
	if err := tenantStore.Bootstrap(ctx); err != nil {
		t.Fatalf("tenant Bootstrap() error: %v", err)
	}
	configStore := tenantconfig.NewStore(conn)
	if err := configStore.Bootstrap(ctx); err != nil {
		t.Fatalf("tenantconfig Bootstrap() error: %v", err)
	}

	mux := http.NewServeMux()
	RegisterConfigRoutes(mux, ConfigDeps{Tenants: tenantStore, Config: configStore})

	return &configTestEnv{mux: mux, tenantStore: tenantStore, conn: conn}
}

func (e *configTestEnv) createTenant(t *testing.T) *tenant.Tenant {
	t.Helper()
	slug := fmt.Sprintf("configroutetest%d", time.Now().UnixNano())
	tt, err := e.tenantStore.CreateTenant(context.Background(), slug, "Config Route Test Co")
	if err != nil {
		t.Fatalf("CreateTenant() error: %v", err)
	}
	t.Cleanup(func() { _, _ = e.conn.Exec("DELETE FROM system.tenants WHERE id = $1", tt.ID) })
	return tt
}

func TestSetConfigRoute_StoresValue(t *testing.T) {
	env := newTestConfigMux(t)
	tt := env.createTenant(t)

	body := `{"key":"engine.mfa_mode","value":"required"}`
	req := httptest.NewRequest(http.MethodPatch, "/admin/tenants/"+tt.Slug+"/config", strings.NewReader(body))
	w := httptest.NewRecorder()
	env.mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s, want 200", w.Code, w.Body.String())
	}

	var stored string
	if err := env.conn.QueryRowContext(context.Background(),
		"SELECT value FROM system.tenant_config_overrides WHERE tenant_id = $1 AND key = $2", tt.ID, "engine.mfa_mode",
	).Scan(&stored); err != nil {
		t.Fatalf("query stored value: %v", err)
	}
	if stored != "required" {
		t.Errorf("stored value = %q, want %q", stored, "required")
	}
}

func TestSetConfigRoute_UnknownTenantReturnsNotFound(t *testing.T) {
	env := newTestConfigMux(t)

	body := `{"key":"engine.mfa_mode","value":"required"}`
	req := httptest.NewRequest(http.MethodPatch, "/admin/tenants/no-such-tenant/config", strings.NewReader(body))
	w := httptest.NewRecorder()
	env.mux.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, body = %s, want 404", w.Code, w.Body.String())
	}
}

func TestSetConfigRoute_MissingKeyReturnsBadRequest(t *testing.T) {
	env := newTestConfigMux(t)
	tt := env.createTenant(t)

	req := httptest.NewRequest(http.MethodPatch, "/admin/tenants/"+tt.Slug+"/config", strings.NewReader(`{"value":"required"}`))
	w := httptest.NewRecorder()
	env.mux.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, body = %s, want 400", w.Code, w.Body.String())
	}
}

func TestSetConfigRoute_UpdatesExistingValue(t *testing.T) {
	env := newTestConfigMux(t)
	tt := env.createTenant(t)

	for _, value := range []string{"optional", "required"} {
		body, err := json.Marshal(map[string]string{"key": "engine.mfa_mode", "value": value})
		if err != nil {
			t.Fatalf("marshal request body: %v", err)
		}
		req := httptest.NewRequest(http.MethodPatch, "/admin/tenants/"+tt.Slug+"/config", strings.NewReader(string(body)))
		w := httptest.NewRecorder()
		env.mux.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, body = %s, want 200", w.Code, w.Body.String())
		}
	}

	var stored string
	if err := env.conn.QueryRowContext(context.Background(),
		"SELECT value FROM system.tenant_config_overrides WHERE tenant_id = $1 AND key = $2", tt.ID, "engine.mfa_mode",
	).Scan(&stored); err != nil {
		t.Fatalf("query stored value: %v", err)
	}
	if stored != "required" {
		t.Errorf("stored value = %q, want %q (the second Set should overwrite the first)", stored, "required")
	}
}
