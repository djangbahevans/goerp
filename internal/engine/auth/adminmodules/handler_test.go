package adminmodules

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json/v2"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
	"uuid"

	"github.com/djangbahevans/goerp/internal/engine/apikey"
	"github.com/djangbahevans/goerp/internal/engine/auth/authcheck"
	"github.com/djangbahevans/goerp/internal/engine/auth/authtest"
	"github.com/djangbahevans/goerp/internal/engine/auth/authtoken"
	"github.com/djangbahevans/goerp/internal/engine/auth/session"
	"github.com/djangbahevans/goerp/internal/engine/auth/sessionrevoke"
	"github.com/djangbahevans/goerp/internal/engine/authaudit"
	"github.com/djangbahevans/goerp/internal/engine/billing"
	"github.com/djangbahevans/goerp/internal/engine/cache"
	"github.com/djangbahevans/goerp/internal/engine/db"
	"github.com/djangbahevans/goerp/internal/engine/manifest"
	"github.com/djangbahevans/goerp/internal/engine/module"
	"github.com/djangbahevans/goerp/internal/engine/permcache"
	"github.com/djangbahevans/goerp/internal/engine/registry"
	"github.com/djangbahevans/goerp/internal/engine/role"
	"github.com/djangbahevans/goerp/internal/engine/route"
	"github.com/djangbahevans/goerp/internal/engine/tenant"
	tenantresolve "github.com/djangbahevans/goerp/internal/engine/tenant/resolve"
	"github.com/djangbahevans/goerp/internal/engine/tenantschema"
	"github.com/djangbahevans/goerp/internal/engine/user"
)

const localPostgresDSN = "postgres://goerp:dev@localhost:15432/goerp"

// Fixture modules: base is a dependency of app, and premium is never
// entitled to the fixture tenant.
const (
	modBase    = "modstestbase"
	modApp     = "modstestapp"
	modPremium = "modstestpremium"
)

type env struct {
	conn     *sql.DB
	tenants  *tenant.Store
	users    *user.Store
	roles    *role.Store
	billing  *billing.Store
	resolver *tenantresolve.Resolver
	issuer   *authtoken.Issuer
	handler  *Handler
}

type fixtureTenant struct {
	id     string
	slug   string
	domain string
}

func newEnv(t *testing.T) *env {
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
	userStore := user.NewStore(conn)
	sessionStore := session.NewStore(conn)
	apiKeys := apikey.NewStore(conn)
	billingStore := billing.NewStore(conn)
	auditStore := authaudit.NewStore(conn, tenantStore)
	for name, bootstrap := range map[string]func(context.Context) error{
		"tenant": tenantStore.Bootstrap, "user": userStore.Bootstrap, "session": sessionStore.Bootstrap,
		"apikey": apiKeys.Bootstrap, "billing": billingStore.Bootstrap, "authaudit": auditStore.Bootstrap,
	} {
		if err := bootstrap(ctx); err != nil {
			t.Fatalf("%s Bootstrap() error: %v", name, err)
		}
	}
	signingKeySet := authtest.SigningKeys()

	modules := &registry.ModuleRegistry{}
	ready := func(name string, dependsOn ...string) *module.LoadedModule {
		return &module.LoadedModule{Status: module.StatusReady, Manifest: manifest.Manifest{
			Name: name, DisplayName: name, Type: "standard", Version: "1.0.0", DependsOn: dependsOn,
			Permissions: []manifest.Permission{{Name: name + ":thing:read", Description: "View things"}},
		}}
	}
	if _, err := modules.Update(map[string]*module.LoadedModule{
		modBase:    ready(modBase),
		modApp:     ready(modApp, modBase),
		modPremium: ready(modPremium),
	}); err != nil {
		t.Fatalf("registry Update() error: %v", err)
	}

	roleStore := role.NewStore(conn)
	revoker := sessionrevoke.NewRevoker(sessionStore, cacheClient)
	checker := authcheck.NewChecker(&signingKeySet.Active, revoker, userStore, roleStore, permcache.NewRoleCache(cacheClient), permcache.NewRolePermissionMap(), apiKeys, false, nil, nil, nil)
	resolver := tenantresolve.NewResolver(tenantStore, cacheClient, billingStore)

	return &env{
		conn:     conn,
		tenants:  tenantStore,
		users:    userStore,
		roles:    roleStore,
		billing:  billingStore,
		resolver: resolver,
		issuer:   authtoken.NewIssuer(&signingKeySet.Active, tenantStore, roleStore, sessionStore),
		handler: NewHandler(Deps{
			Tenants:  resolver,
			Auth:     checker,
			Registry: modules,
			Settings: billingStore,
			Cache:    cacheClient,
			Audit:    auditStore,
		}),
	}
}

func (e *env) newTenant(t *testing.T) fixtureTenant {
	t.Helper()
	ctx := t.Context()

	slug := fmt.Sprintf("adminmodulestest%d", time.Now().UnixNano())
	tt, err := e.tenants.CreateTenant(ctx, slug, "Admin Modules Test Co")
	if err != nil {
		t.Fatalf("CreateTenant() error: %v", err)
	}
	t.Cleanup(func() {
		_, _ = e.conn.Exec(`DELETE FROM system.auth_audit_log WHERE tenant_id = $1`, tt.ID)
		_, _ = e.conn.Exec(`DELETE FROM system.tenants WHERE id = $1`, tt.ID)
	})
	if _, err := e.tenants.UpdateStatus(ctx, slug, tenant.StatusActive, nil); err != nil {
		t.Fatalf("activate fixture tenant: %v", err)
	}
	domain := slug + ".goerp.test"
	if _, err := e.tenants.CreateDomain(ctx, tt.ID, domain, tenant.DomainSubdomain, true); err != nil {
		t.Fatalf("CreateDomain() error: %v", err)
	}
	for _, name := range []string{modBase, modApp} {
		if err := e.billing.UpsertEntitlementOverride(ctx, tt.ID, "module."+name, "true", nil, nil, nil); err != nil {
			t.Fatalf("entitle %s: %v", name, err)
		}
	}

	schema := tenantschema.Name(slug)
	if _, err := e.conn.Exec("CREATE SCHEMA " + schema); err != nil {
		t.Fatalf("create fixture schema: %v", err)
	}
	t.Cleanup(func() { _, _ = e.conn.Exec(fmt.Sprintf("DROP SCHEMA %s CASCADE", schema)) })
	if err := e.roles.Bootstrap(ctx, slug); err != nil {
		t.Fatalf("role Bootstrap() error: %v", err)
	}
	if err := e.roles.SeedBuiltinRoles(ctx, slug); err != nil {
		t.Fatalf("SeedBuiltinRoles() error: %v", err)
	}
	return fixtureTenant{id: tt.ID, slug: slug, domain: domain}
}

// token returns an access token for a new active member holding roleName.
func (e *env) token(t *testing.T, ft fixtureTenant, roleName string) string {
	t.Helper()
	ctx := t.Context()
	id, err := e.users.FindOrCreateInvited(ctx, fmt.Sprintf("adminmodules%d@example.com", time.Now().UnixNano()))
	if err != nil {
		t.Fatalf("FindOrCreateInvited() error: %v", err)
	}
	t.Cleanup(func() {
		_, _ = e.conn.Exec(`DELETE FROM system.sessions WHERE user_id = $1`, id)
		_, _ = e.conn.Exec(`DELETE FROM system.users WHERE id = $1`, id)
	})
	if _, err := e.conn.Exec(`UPDATE system.users SET status = 'active' WHERE id = $1`, id); err != nil {
		t.Fatalf("activate fixture user: %v", err)
	}
	roleID, err := e.roles.GetRoleByName(ctx, ft.slug, roleName)
	if err != nil {
		t.Fatalf("GetRoleByName(%q) error: %v", roleName, err)
	}
	if err := e.roles.AddMember(ctx, ft.slug, id); err != nil {
		t.Fatalf("AddMember() error: %v", err)
	}
	if err := e.roles.AssignRole(ctx, ft.slug, id, roleID, ""); err != nil {
		t.Fatalf("AssignRole() error: %v", err)
	}
	tokens, err := e.issuer.Issue(ctx, authtoken.LoginParams{UserID: id, TenantSlug: ft.slug, DeviceID: uuid.New().String()})
	if err != nil {
		t.Fatalf("Issue() error: %v", err)
	}
	return tokens.AccessToken
}

func (e *env) auditCount(t *testing.T, ft fixtureTenant, eventType string) int {
	t.Helper()
	var n int
	if err := e.conn.QueryRow(`SELECT COUNT(*) FROM system.auth_audit_log WHERE tenant_id = $1 AND event_type = $2`, ft.id, eventType).Scan(&n); err != nil {
		t.Fatalf("count audit rows: %v", err)
	}
	return n
}

func (e *env) list(t *testing.T, ft fixtureTenant, token string) *httptest.ResponseRecorder {
	t.Helper()
	return do(t, ft, token, e.handler.ServeList, http.MethodGet, "/admin/modules", "", nil)
}

func (e *env) set(t *testing.T, ft fixtureTenant, token, name string, body any) *httptest.ResponseRecorder {
	t.Helper()
	return do(t, ft, token, e.handler.ServePatchSettings, http.MethodPatch, "/admin/modules/"+name+"/settings", name, body)
}

func do(t *testing.T, ft fixtureTenant, token string, serve http.HandlerFunc, method, path, name string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		if err := json.MarshalWrite(&buf, body); err != nil {
			t.Fatalf("marshal body: %v", err)
		}
	}
	r := httptest.NewRequest(method, path, &buf)
	r.Host = ft.domain
	r.RemoteAddr = "203.0.113.7:54321"
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	if name != "" {
		r = r.WithContext(route.WithParams(r.Context(), map[string]string{"name": name}))
	}
	rec := httptest.NewRecorder()
	serve(rec, r)
	return rec
}

func decode[T any](t *testing.T, rec *httptest.ResponseRecorder) T {
	t.Helper()
	var out T
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode %s: %v", rec.Body, err)
	}
	return out
}

type errorBody struct {
	Error struct {
		Code    string `json:"code"`
		Details struct {
			Modules []string `json:"modules"`
		} `json:"details"`
	} `json:"error"`
}

func requireStatus(t *testing.T, rec *httptest.ResponseRecorder, status int, code string) errorBody {
	t.Helper()
	if rec.Code != status {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, status, rec.Body)
	}
	body := decode[errorBody](t, rec)
	if code != "" && body.Error.Code != code {
		t.Errorf("error code = %q, want %q", body.Error.Code, code)
	}
	return body
}

func findModule(t *testing.T, rec *httptest.ResponseRecorder, name string) moduleJSON {
	t.Helper()
	for _, m := range decode[struct {
		Modules []moduleJSON `json:"modules"`
	}](t, rec).Modules {
		if m.Name == name {
			return m
		}
	}
	t.Fatalf("module %q not in %s", name, rec.Body)
	return moduleJSON{}
}

func TestList_ReportsEntitlementAndEnabledState(t *testing.T) {
	e := newEnv(t)
	ft := e.newTenant(t)
	admin := e.token(t, ft, "admin")

	rec := e.list(t, ft, admin)
	requireStatus(t, rec, http.StatusOK, "")

	app := findModule(t, rec, modApp)
	if !app.Entitled || !app.Enabled || len(app.DependsOn) != 1 || app.DependsOn[0] != modBase {
		t.Errorf("%s = %+v, want entitled and enabled, depending on %s", modApp, app, modBase)
	}
	if len(app.Permissions) != 1 || app.Permissions[0].Name != modApp+":thing:read" {
		t.Errorf("%s permissions = %+v", modApp, app.Permissions)
	}
	premium := findModule(t, rec, modPremium)
	if premium.Entitled || premium.Enabled {
		t.Errorf("%s = %+v, want neither entitled nor enabled", modPremium, premium)
	}
}

func TestAuthorization(t *testing.T) {
	e := newEnv(t)
	ft := e.newTenant(t)

	requireStatus(t, e.list(t, ft, ""), http.StatusUnauthorized, "unauthenticated")
	requireStatus(t, e.set(t, ft, "", modApp, map[string]any{"enabled": false}), http.StatusUnauthorized, "unauthenticated")

	user := e.token(t, ft, "user")
	requireStatus(t, e.list(t, ft, user), http.StatusForbidden, "forbidden")
	requireStatus(t, e.set(t, ft, user, modApp, map[string]any{"enabled": false}), http.StatusForbidden, "forbidden")
}

func TestPatchSettings_DisableAndReenable(t *testing.T) {
	e := newEnv(t)
	ft := e.newTenant(t)
	admin := e.token(t, ft, "admin")
	ctx := t.Context()

	if _, err := e.resolver.LoadEntitlements(ctx, ft.id); err != nil {
		t.Fatalf("prime LoadEntitlements() error: %v", err)
	}

	rec := e.set(t, ft, admin, modApp, map[string]any{"enabled": false})
	requireStatus(t, rec, http.StatusOK, "")
	got := decode[moduleJSON](t, rec)
	if got.Enabled || !got.Entitled {
		t.Errorf("response = %+v, want entitled but disabled", got)
	}
	ents, err := e.resolver.LoadEntitlements(ctx, ft.id)
	if err != nil {
		t.Fatalf("LoadEntitlements() error: %v", err)
	}
	if ents.ModuleEnabled(modApp) || !ents.ModuleDisabledByTenant(modApp) {
		t.Error("entitlements still enable the module after the cache should have been invalidated")
	}
	if n := e.auditCount(t, ft, "module.disabled"); n != 1 {
		t.Errorf("module.disabled audit rows = %d, want 1", n)
	}

	requireStatus(t, e.set(t, ft, admin, modApp, map[string]any{"enabled": false}), http.StatusOK, "")
	if n := e.auditCount(t, ft, "module.disabled"); n != 1 {
		t.Errorf("module.disabled audit rows after a repeated disable = %d, want 1", n)
	}

	rec = e.set(t, ft, admin, modApp, map[string]any{"enabled": true})
	requireStatus(t, rec, http.StatusOK, "")
	if got := decode[moduleJSON](t, rec); !got.Enabled {
		t.Errorf("response = %+v, want enabled", got)
	}
	ents, err = e.resolver.LoadEntitlements(ctx, ft.id)
	if err != nil {
		t.Fatalf("LoadEntitlements() error: %v", err)
	}
	if !ents.ModuleEnabled(modApp) || ents.ModuleDisabledByTenant(modApp) {
		t.Error("entitlements do not enable the module after re-enabling it")
	}
	if n := e.auditCount(t, ft, "module.enabled"); n != 1 {
		t.Errorf("module.enabled audit rows = %d, want 1", n)
	}
}

func TestPatchSettings_DependencyConflicts(t *testing.T) {
	e := newEnv(t)
	ft := e.newTenant(t)
	admin := e.token(t, ft, "admin")

	body := requireStatus(t, e.set(t, ft, admin, modBase, map[string]any{"enabled": false}), http.StatusConflict, "module_has_dependents")
	if len(body.Error.Details.Modules) != 1 || body.Error.Details.Modules[0] != modApp {
		t.Errorf("dependents = %v, want [%s]", body.Error.Details.Modules, modApp)
	}

	requireStatus(t, e.set(t, ft, admin, modApp, map[string]any{"enabled": false}), http.StatusOK, "")
	requireStatus(t, e.set(t, ft, admin, modBase, map[string]any{"enabled": false}), http.StatusOK, "")

	body = requireStatus(t, e.set(t, ft, admin, modApp, map[string]any{"enabled": true}), http.StatusConflict, "module_dependency_disabled")
	if len(body.Error.Details.Modules) != 1 || body.Error.Details.Modules[0] != modBase {
		t.Errorf("disabled dependencies = %v, want [%s]", body.Error.Details.Modules, modBase)
	}
}

func TestPatchSettings_RejectsInvalidRequests(t *testing.T) {
	e := newEnv(t)
	ft := e.newTenant(t)
	admin := e.token(t, ft, "admin")

	requireStatus(t, e.set(t, ft, admin, modPremium, map[string]any{"enabled": true}), http.StatusConflict, "module_not_entitled")
	requireStatus(t, e.set(t, ft, admin, modPremium, map[string]any{"enabled": false}), http.StatusConflict, "module_not_entitled")
	requireStatus(t, e.set(t, ft, admin, "nosuchmodule", map[string]any{"enabled": true}), http.StatusNotFound, "not_found")
	requireStatus(t, e.set(t, ft, admin, modApp, map[string]any{}), http.StatusBadRequest, "invalid_request")
	requireStatus(t, e.set(t, ft, admin, modApp, map[string]any{"enabled": "yes"}), http.StatusBadRequest, "invalid_request")
}
