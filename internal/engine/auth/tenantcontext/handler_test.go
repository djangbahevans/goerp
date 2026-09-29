package tenantcontext

import (
	"context"
	"encoding/json/v2"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/auth/password"
	"github.com/djangbahevans/goerp/internal/engine/billing"
	"github.com/djangbahevans/goerp/internal/engine/cache"
	"github.com/djangbahevans/goerp/internal/engine/db"
	"github.com/djangbahevans/goerp/internal/engine/role"
	"github.com/djangbahevans/goerp/internal/engine/tenant"
	tenantresolve "github.com/djangbahevans/goerp/internal/engine/tenant/resolve"
	"github.com/djangbahevans/goerp/internal/engine/tenantconfig"
)

const localPostgresDSN = "postgres://goerp:dev@localhost:15432/goerp"

type fixture struct {
	handler     *Handler
	tenantStore *tenant.Store
	cache       *cache.Client
	slug        string
	domain      string
	resolver    *tenantresolve.Resolver
	sharedHost  string
	appBaseURL  string
	tenantID    string
	config      *tenantconfig.Store
	policies    *password.PolicyStore
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
	configStore := tenantconfig.NewStore(conn)
	if err := configStore.Bootstrap(ctx); err != nil {
		t.Fatalf("tenantconfig Bootstrap() error: %v", err)
	}
	policies := password.NewPolicyStore(configStore, role.NewStore(conn))
	sharedHost := "shared-" + slug + ".goerp.test"
	appBaseURL := "https://" + sharedHost
	return &fixture{
		handler:  NewHandler(resolver, Config{AppBaseURL: appBaseURL, Policies: policies}),
		resolver: resolver, tenantStore: tenantStore, cache: cacheClient,
		slug: slug, domain: domain, sharedHost: sharedHost, appBaseURL: appBaseURL,
		tenantID: tt.ID, config: configStore, policies: policies,
	}
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
	if v, ok := body["terms_url"]; !ok || v != nil {
		t.Errorf("terms_url = %v (present=%v), want explicit null when unconfigured", v, ok)
	}
	if body["app_url"] != f.appBaseURL {
		t.Errorf("app_url = %v, want %q", body["app_url"], f.appBaseURL)
	}
}

func TestServeHTTP_SharedHostReturnsNullTenant(t *testing.T) {
	f := newFixture(t)

	for _, host := range []string{f.sharedHost, strings.ToUpper(f.sharedHost) + ":5173"} {
		rec, body := f.get(t, host)

		if rec.Code != http.StatusOK {
			t.Fatalf("host %q: status = %d, body = %s, want 200", host, rec.Code, rec.Body.String())
		}
		if v, ok := body["tenant"]; !ok || v != nil {
			t.Errorf("host %q: tenant = %v (present=%v), want explicit null", host, v, ok)
		}
		if body["app_url"] != f.appBaseURL {
			t.Errorf("host %q: app_url = %v, want %q", host, body["app_url"], f.appBaseURL)
		}
	}
}

func TestServeHTTP_UnknownHostReturns404WithAppURL(t *testing.T) {
	f := newFixture(t)

	rec, body := f.get(t, "typo-"+f.slug+".goerp.test")

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, body = %s, want 404", rec.Code, rec.Body.String())
	}
	errBody, _ := body["error"].(map[string]any)
	if errBody["code"] != "tenant_not_found" {
		t.Errorf("error.code = %v, want tenant_not_found", errBody["code"])
	}
	details, _ := errBody["details"].(map[string]any)
	if details["app_url"] != f.appBaseURL {
		t.Errorf("error.details.app_url = %v, want %q", details["app_url"], f.appBaseURL)
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
	f.handler = NewHandler(f.resolver, Config{RegistrationEnabled: true, AppBaseURL: f.appBaseURL, Policies: f.policies})

	_, resolved := f.get(t, f.domain)
	_, shared := f.get(t, f.sharedHost)

	if resolved["registration_enabled"] != true || shared["registration_enabled"] != true {
		t.Errorf("registration_enabled = %v (resolved), %v (shared), want true for both", resolved["registration_enabled"], shared["registration_enabled"])
	}
}

func TestServeHTTP_ReportsConfiguredTermsURL(t *testing.T) {
	f := newFixture(t)
	const termsURL = "https://example.com/terms"
	f.handler = NewHandler(f.resolver, Config{RegistrationEnabled: true, TermsURL: termsURL, AppBaseURL: f.appBaseURL, Policies: f.policies})

	_, resolved := f.get(t, f.domain)
	_, shared := f.get(t, f.sharedHost)

	if resolved["terms_url"] != termsURL || shared["terms_url"] != termsURL {
		t.Errorf("terms_url = %v (resolved), %v (shared), want %q for both", resolved["terms_url"], shared["terms_url"], termsURL)
	}
}

func TestServeHTTP_ReportsTheEffectivePasswordMinLength(t *testing.T) {
	f := newFixture(t)
	if err := f.config.Set(t.Context(), f.tenantID, password.KeyMinLength, "14"); err != nil {
		t.Fatalf("Set() policy error: %v", err)
	}

	_, resolved := f.get(t, f.domain)
	_, shared := f.get(t, f.sharedHost)

	if resolved["password_min_length"] != float64(14) {
		t.Errorf("resolved password_min_length = %v, want the tenant's 14", resolved["password_min_length"])
	}
	if shared["password_min_length"] != float64(password.Global.MinLength) {
		t.Errorf("shared password_min_length = %v, want the global %d", shared["password_min_length"], password.Global.MinLength)
	}
}
