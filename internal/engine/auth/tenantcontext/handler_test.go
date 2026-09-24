package tenantcontext

import (
	"context"
	"encoding/json/v2"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/billing"
	"github.com/djangbahevans/goerp/internal/engine/cache"
	"github.com/djangbahevans/goerp/internal/engine/db"
	"github.com/djangbahevans/goerp/internal/engine/tenant"
	tenantresolve "github.com/djangbahevans/goerp/internal/engine/tenant/resolve"
)

const localPostgresDSN = "postgres://goerp:dev@localhost:55432/goerp"

type fixture struct {
	handler     *Handler
	tenantStore *tenant.Store
	cache       *cache.Client
	slug        string
	domain      string
	resolver    *tenantresolve.Resolver
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	ctx := t.Context()

	conn, err := db.New(localPostgresDSN)
	if err != nil {
		t.Skipf("postgres not reachable at %s (start compose.dev.yml): %v", localPostgresDSN, err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	cacheClient, err := cache.New(ctx, cache.Config{Addr: "localhost:6379", DB: 0, MaxRetries: 1})
	if err != nil {
		t.Skipf("redis not reachable at localhost:6379 (start compose.dev.yml): %v", err)
	}
	t.Cleanup(func() { _ = cacheClient.Close() })

	tenantStore := tenant.NewStore(conn)
	if err := tenantStore.Bootstrap(ctx); err != nil {
		t.Fatalf("tenant Bootstrap() error: %v", err)
	}
	billingStore := billing.NewStore(conn)
	if err := billingStore.Bootstrap(ctx); err != nil {
		t.Fatalf("billing Bootstrap() error: %v", err)
	}

	slug := fmt.Sprintf("tenantctxtest%d", time.Now().UnixNano())
	tt, err := tenantStore.CreateTenant(ctx, slug, "Tenant Context Test Co")
	if err != nil {
		t.Fatalf("CreateTenant() error: %v", err)
	}
	t.Cleanup(func() { _, _ = conn.Exec(`DELETE FROM system.tenants WHERE id = $1`, tt.ID) })
	if _, err := tenantStore.UpdateStatus(ctx, slug, tenant.StatusActive, nil); err != nil {
		t.Fatalf("activate fixture tenant: %v", err)
	}
	domain := slug + ".goerp.test"
	if _, err := tenantStore.CreateDomain(ctx, tt.ID, domain, tenant.DomainSubdomain, true); err != nil {
		t.Fatalf("CreateDomain() error: %v", err)
	}
	t.Cleanup(func() { _ = cacheClient.Delete(context.Background(), tenantresolve.DomainCacheKey(domain)) })

	resolver := tenantresolve.NewResolver(tenantStore, cacheClient, billingStore)
	return &fixture{handler: NewHandler(resolver, false), resolver: resolver, tenantStore: tenantStore, cache: cacheClient, slug: slug, domain: domain}
}

func (f *fixture) get(t *testing.T, host string) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/auth/tenant-context", nil)
	req.Host = host
	rec := httptest.NewRecorder()
	f.handler.ServeHTTP(rec, req)
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body %q: %v", rec.Body.String(), err)
	}
	return rec, body
}

func TestServeHTTP_ResolvedHostReturnsTenant(t *testing.T) {
	f := newFixture(t)

	rec, body := f.get(t, f.domain)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s, want 200", rec.Code, rec.Body.String())
	}
	tenantBody, _ := body["tenant"].(map[string]any)
	if tenantBody["slug"] != f.slug || tenantBody["name"] != "Tenant Context Test Co" {
		t.Errorf("tenant = %v, want slug %q and name %q", body["tenant"], f.slug, "Tenant Context Test Co")
	}
	if body["registration_enabled"] != false {
		t.Errorf("registration_enabled = %v, want false", body["registration_enabled"])
	}
}

func TestServeHTTP_UnresolvedHostReturnsNullTenant(t *testing.T) {
	f := newFixture(t)

	rec, body := f.get(t, "shared-"+f.slug+".goerp.test")

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s, want 200", rec.Code, rec.Body.String())
	}
	if v, ok := body["tenant"]; !ok || v != nil {
		t.Errorf("tenant = %v (present=%v), want explicit null", v, ok)
	}
}

func TestServeHTTP_SuspendedTenantReturns403(t *testing.T) {
	f := newFixture(t)
	if _, err := f.tenantStore.UpdateStatus(t.Context(), f.slug, tenant.StatusSuspended, nil); err != nil {
		t.Fatalf("suspend fixture tenant: %v", err)
	}

	rec, body := f.get(t, f.domain)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, body = %s, want 403", rec.Code, rec.Body.String())
	}
	errBody, _ := body["error"].(map[string]any)
	if errBody["code"] != "tenant_suspended" {
		t.Errorf("error.code = %v, want tenant_suspended", errBody["code"])
	}
}

func TestServeHTTP_ReportsPlatformRegistrationSetting(t *testing.T) {
	f := newFixture(t)
	f.handler = NewHandler(f.resolver, true)

	_, resolved := f.get(t, f.domain)
	_, shared := f.get(t, "shared-"+f.slug+".goerp.test")

	if resolved["registration_enabled"] != true || shared["registration_enabled"] != true {
		t.Errorf("registration_enabled = %v (resolved), %v (shared), want true for both", resolved["registration_enabled"], shared["registration_enabled"])
	}
}
