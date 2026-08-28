package engine

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/apikey"
	"github.com/djangbahevans/goerp/internal/engine/auth/authcheck"
	"github.com/djangbahevans/goerp/internal/engine/auth/authtoken"
	"github.com/djangbahevans/goerp/internal/engine/auth/mfatoken"
	"github.com/djangbahevans/goerp/internal/engine/auth/session"
	"github.com/djangbahevans/goerp/internal/engine/auth/sessionrevoke"
	"github.com/djangbahevans/goerp/internal/engine/auth/signingkey"
	"github.com/djangbahevans/goerp/internal/engine/billing"
	"github.com/djangbahevans/goerp/internal/engine/cache"
	"github.com/djangbahevans/goerp/internal/engine/db"
	"github.com/djangbahevans/goerp/internal/engine/manifest"
	"github.com/djangbahevans/goerp/internal/engine/mfa"
	"github.com/djangbahevans/goerp/internal/engine/mfa/enforce"
	"github.com/djangbahevans/goerp/internal/engine/module"
	"github.com/djangbahevans/goerp/internal/engine/permcache"
	"github.com/djangbahevans/goerp/internal/engine/registry"
	"github.com/djangbahevans/goerp/internal/engine/role"
	"github.com/djangbahevans/goerp/internal/engine/route"
	"github.com/djangbahevans/goerp/internal/engine/secrets"
	"github.com/djangbahevans/goerp/internal/engine/tenant"
	tenantresolve "github.com/djangbahevans/goerp/internal/engine/tenant/resolve"
	"github.com/djangbahevans/goerp/internal/engine/tenantconfig"
	"github.com/djangbahevans/goerp/internal/engine/tenantschema"
	"github.com/djangbahevans/goerp/internal/engine/user"
	sdkengine "github.com/djangbahevans/goerp/sdk/go/engine"
	"go.opentelemetry.io/otel/trace/noop"
)

const chainTestPostgresDSN = "postgres://goerp:dev@localhost:55432/goerp"
const chainTestRedisAddr = "localhost:6379"
const chainTestPermission = "widgets.read"

// chainTestUngrantedPermission is declared in the fixture module's
// manifest (so it has a real index in both the PermissionRegistry and the
// permcache.RolePermissionMap the fixture builds once, in lockstep —
// authcheck.Checker's own doc comment explains why those two must always
// share one registry generation) but never granted to the fixture's admin
// role, for testing the permission-denied path without a runtime
// grant/revoke that a already-built RolePermissionMap snapshot wouldn't
// see anyway.
const chainTestUngrantedPermission = "widgets.delete"

// chainFixture is a real tenant + user + module route, wired against the
// same Postgres/Redis/authcheck.Checker/tenantresolve.Resolver stack the
// rest of this codebase's integration tests use (no mocks) — enough to
// drive requests through buildChain end-to-end.
type chainFixture struct {
	conn        *sql.DB
	cacheClient *cache.Client
	resolver    *tenantresolve.Resolver
	checker     *authcheck.Checker
	issuer      *authtoken.Issuer
	reg         *registry.ModuleRegistry
	tenantID    string
	tenantSlug  string
	domain      string
	userID      string
}

func newChainFixture(t *testing.T) *chainFixture {
	t.Helper()
	ctx := context.Background()

	conn, err := db.New(chainTestPostgresDSN)
	if err != nil {
		t.Skipf("postgres not reachable at %s (start compose.dev.yml): %v", chainTestPostgresDSN, err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	lockChainSigningKeyTable(t, conn)

	cacheClient, err := cache.New(ctx, cache.Config{Addr: chainTestRedisAddr, DB: 0, MaxRetries: 1})
	if err != nil {
		t.Skipf("redis not reachable at %s (start compose.dev.yml): %v", chainTestRedisAddr, err)
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
	userStore := user.NewStore(conn)
	if err := userStore.Bootstrap(ctx); err != nil {
		t.Fatalf("user Bootstrap() error: %v", err)
	}
	sessionStore := session.NewStore(conn)
	if err := sessionStore.Bootstrap(ctx); err != nil {
		t.Fatalf("session Bootstrap() error: %v", err)
	}
	signingKeyStore := signingkey.NewStore(conn, &secrets.EnvBackend{})
	if err := signingKeyStore.Bootstrap(ctx); err != nil {
		t.Fatalf("signingkey Bootstrap() error: %v", err)
	}
	keySet, err := signingKeyStore.LoadOrGenerate(ctx)
	if err != nil {
		t.Fatalf("LoadOrGenerate() error: %v", err)
	}
	mfaTokenKeyStore := mfatoken.NewStore(conn, &secrets.EnvBackend{})
	if err := mfaTokenKeyStore.Bootstrap(ctx); err != nil {
		t.Fatalf("mfatoken Bootstrap() error: %v", err)
	}
	mfaTokenKeySet, err := mfaTokenKeyStore.LoadOrGenerate(ctx)
	if err != nil {
		t.Fatalf("mfatoken LoadOrGenerate() error: %v", err)
	}
	mfaCreds := mfa.NewStore(conn)
	if err := mfaCreds.Bootstrap(ctx); err != nil {
		t.Fatalf("mfa Bootstrap() error: %v", err)
	}
	tenantConfigStore := tenantconfig.NewStore(conn)
	if err := tenantConfigStore.Bootstrap(ctx); err != nil {
		t.Fatalf("tenantconfig Bootstrap() error: %v", err)
	}
	apiKeys := apikey.NewStore(conn)
	if err := apiKeys.Bootstrap(ctx); err != nil {
		t.Fatalf("apikey Bootstrap() error: %v", err)
	}

	slug := fmt.Sprintf("chaintest%d", time.Now().UnixNano())
	tt, err := tenantStore.CreateTenant(ctx, slug, "Chain Test Co")
	if err != nil {
		t.Fatalf("CreateTenant() error: %v", err)
	}
	t.Cleanup(func() { _, _ = conn.Exec(`DELETE FROM system.tenants WHERE id = $1`, tt.ID) })
	if _, err := tenantStore.UpdateStatus(ctx, slug, tenant.StatusActive, nil); err != nil {
		t.Fatalf("activate fixture tenant: %v", err)
	}

	// Entitles the fixture tenant to the "widgets" module — goerp#441's
	// dispatch-gating check would otherwise 403 billing.module_not_available
	// before any of this file's own module-dispatch tests reach the
	// module_unavailable/auth/permission behavior they're actually testing.
	plan, err := billingStore.CreatePlan(ctx, "plan-"+slug, "Chain Test Plan", nil, nil)
	if err != nil {
		t.Fatalf("CreatePlan() error: %v", err)
	}
	if err := billingStore.UpsertPlanEntitlement(ctx, plan.ID, "module.widgets", "true"); err != nil {
		t.Fatalf("UpsertPlanEntitlement() error: %v", err)
	}
	now := time.Now()
	if _, err := billingStore.CreateSubscription(ctx, tt.ID, plan.ID, now, now.Add(time.Hour)); err != nil {
		t.Fatalf("CreateSubscription() error: %v", err)
	}

	domain := slug + ".example.com"
	if _, err := conn.Exec(`INSERT INTO system.tenant_domains (tenant_id, domain, type) VALUES ($1, $2, 'subdomain')`, tt.ID, domain); err != nil {
		t.Fatalf("insert tenant domain: %v", err)
	}

	userID, err := userStore.FindOrCreateInvited(ctx, slug+"@example.com")
	if err != nil {
		t.Fatalf("FindOrCreateInvited() error: %v", err)
	}
	if _, err := conn.Exec(`UPDATE system.users SET status = 'active' WHERE id = $1`, userID); err != nil {
		t.Fatalf("activate fixture user: %v", err)
	}
	t.Cleanup(func() { _, _ = conn.Exec(`DELETE FROM system.users WHERE id = $1`, userID) })
	t.Cleanup(func() { _, _ = conn.Exec(`DELETE FROM system.sessions WHERE user_id = $1`, userID) })
	t.Cleanup(func() { _, _ = conn.Exec(`DELETE FROM system.user_mfa WHERE user_id = $1`, userID) })

	schema := tenantschema.Name(slug)
	if _, err := conn.Exec("CREATE SCHEMA " + schema); err != nil {
		t.Fatalf("create fixture schema: %v", err)
	}
	t.Cleanup(func() { _, _ = conn.Exec(fmt.Sprintf("DROP SCHEMA %s CASCADE", schema)) })

	roleStore := role.NewStore(conn)
	if err := roleStore.Bootstrap(ctx, slug); err != nil {
		t.Fatalf("role Bootstrap() error: %v", err)
	}
	if err := roleStore.SeedBuiltinRoles(ctx, slug); err != nil {
		t.Fatalf("SeedBuiltinRoles() error: %v", err)
	}
	roleID, err := roleStore.GetRoleByName(ctx, slug, "admin")
	if err != nil {
		t.Fatalf("GetRoleByName() error: %v", err)
	}
	if _, err := conn.Exec(fmt.Sprintf("INSERT INTO %s.user_roles (user_id, role_id) VALUES ($1, $2)", schema), userID, roleID); err != nil {
		t.Fatalf("grant admin role: %v", err)
	}
	if _, err := conn.Exec(fmt.Sprintf("INSERT INTO %s.role_permissions (role_id, permission_name) VALUES ($1, $2)", schema), roleID, chainTestPermission); err != nil {
		t.Fatalf("grant permission: %v", err)
	}

	roleMap := permcache.NewRolePermissionMap()
	reg := &registry.ModuleRegistry{}
	loadedModules := map[string]*module.LoadedModule{
		"widgets": {
			Manifest: manifest.Manifest{
				Type:        "standard",
				Permissions: []manifest.Permission{{Name: chainTestPermission}, {Name: chainTestUngrantedPermission}},
			},
			ExplicitRoutes: []sdkengine.RouteDeclaration{
				{Method: http.MethodGet, Path: "/items", Auth: "required", Permissions: []string{chainTestPermission}},
				{Method: http.MethodGet, Path: "/forbidden", Auth: "required", Permissions: []string{chainTestUngrantedPermission}},
			},
		},
	}
	if _, err := reg.Update(loadedModules); err != nil {
		t.Fatalf("registry Update() error: %v", err)
	}
	snap := reg.Snapshot()
	if err := roleMap.RebuildAll(ctx, tenantStore, roleStore, snap.PermissionRegistry()); err != nil {
		t.Fatalf("RebuildAll() error: %v", err)
	}
	roleCache := permcache.NewRoleCache(cacheClient)

	resolver := tenantresolve.NewResolver(tenantStore, cacheClient, billingStore)
	mfaTokens := mfatoken.NewCodec(&mfaTokenKeySet.Active)
	mfaPolicies := enforce.NewStore(tenantConfigStore)
	checker := authcheck.NewChecker(&keySet.Active, sessionrevoke.NewRevoker(sessionStore, cacheClient), userStore, roleStore, roleCache, roleMap, apiKeys, false, mfaTokens, mfaCreds, mfaPolicies)
	issuer := authtoken.NewIssuer(&keySet.Active, tenantStore, roleStore, sessionStore)

	return &chainFixture{
		conn:        conn,
		cacheClient: cacheClient,
		resolver:    resolver,
		checker:     checker,
		issuer:      issuer,
		reg:         reg,
		tenantID:    tt.ID,
		tenantSlug:  slug,
		domain:      domain,
		userID:      userID,
	}
}

func lockChainSigningKeyTable(t *testing.T, pool *sql.DB) {
	t.Helper()
	ctx := context.Background()
	key := db.AdvisoryLockKey("test.jwt_signing_keys_table")

	conn, err := pool.Conn(ctx)
	if err != nil {
		t.Fatalf("acquire dedicated connection for signing-key lock: %v", err)
	}
	if _, err := conn.ExecContext(ctx, "SELECT pg_advisory_lock($1)", key); err != nil {
		t.Fatalf("acquire signing-key advisory lock: %v", err)
	}
	t.Cleanup(func() {
		_, _ = conn.ExecContext(context.Background(), "SELECT pg_advisory_unlock($1)", key)
		_ = conn.Close()
	})

	mfaKey := db.AdvisoryLockKey("test.mfa_token_signing_keys_table")
	mfaConn, err := pool.Conn(ctx)
	if err != nil {
		t.Fatalf("acquire dedicated connection for mfa-token-key lock: %v", err)
	}
	if _, err := mfaConn.ExecContext(ctx, "SELECT pg_advisory_lock($1)", mfaKey); err != nil {
		t.Fatalf("acquire mfa-token-key advisory lock: %v", err)
	}
	t.Cleanup(func() {
		_, _ = mfaConn.ExecContext(context.Background(), "SELECT pg_advisory_unlock($1)", mfaKey)
		_ = mfaConn.Close()
	})
}

func (f *chainFixture) issueToken(t *testing.T) string {
	t.Helper()
	tokens, err := f.issuer.Issue(context.Background(), authtoken.LoginParams{
		UserID:     f.userID,
		TenantSlug: f.tenantSlug,
		DeviceID:   "11111111-1111-1111-1111-111111111111",
	})
	if err != nil {
		t.Fatalf("Issue() error: %v", err)
	}
	return tokens.AccessToken
}

func (f *chainFixture) chain(builtins map[string]http.Handler) http.Handler {
	// A generous default so existing tests firing several requests in a
	// row don't trip the limiter incidentally — rate limiting itself is
	// covered by its own dedicated tests using a deliberately tight
	// limit.
	generousDefault := route.RateLimitConfig{Requests: 10000, WindowSeconds: 60, Scope: "ip"}
	// The fixture's "widgets" module is always left at its zero-value
	// Status (never StatusReady), so every test in this file hits the
	// module_unavailable gate in buildDispatchHandler before touching any
	// *Engine field — a zero-value Engine is enough here.
	return buildChain(&Engine{}, f.reg, builtins, nil, f.resolver, f.checker, noop.NewTracerProvider().Tracer("test"), f.cacheClient, generousDefault)
}

func decodeErrorCode(t *testing.T, w *httptest.ResponseRecorder) string {
	t.Helper()
	var body routeErrorEnvelope
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode error response: %v", err)
	}
	return body.Error.Code
}

func TestBuildChain_ModuleRouteRequiredAuthNoTokenReturns401(t *testing.T) {
	f := newChainFixture(t)
	h := f.chain(nil)

	req := httptest.NewRequest(http.MethodGet, "/widgets/items", nil)
	req.Host = f.domain
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401; body: %s", w.Code, w.Body.String())
	}
	if code := decodeErrorCode(t, w); code != "unauthenticated" {
		t.Errorf("error.code = %q, want %q", code, "unauthenticated")
	}
}

func TestBuildChain_ModuleRouteValidJWTReachesDispatchWith503(t *testing.T) {
	f := newChainFixture(t)
	h := f.chain(nil)
	token := f.issueToken(t)

	req := httptest.NewRequest(http.MethodGet, "/widgets/items", nil)
	req.Host = f.domain
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	// The fixture's "widgets" module is left at its zero-value Status
	// (never StatusReady) — reaching the terminal handler's
	// module_unavailable, rather than a 401/403/404 from any auth/tenant
	// middleware, is what proves the JWT branch populated a valid
	// AuthContext/TenantContext and every step let the request through.
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503 (module_unavailable); body: %s", w.Code, w.Body.String())
	}
	if code := decodeErrorCode(t, w); code != "module_unavailable" {
		t.Errorf("error.code = %q, want %q", code, "module_unavailable")
	}
}

func TestBuildChain_ModuleRouteUnresolvedTenantReturns404(t *testing.T) {
	f := newChainFixture(t)
	h := f.chain(nil)

	req := httptest.NewRequest(http.MethodGet, "/widgets/items", nil)
	req.Host = "no-such-tenant.example.com"
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body: %s", w.Code, w.Body.String())
	}
	if code := decodeErrorCode(t, w); code != "not_found" {
		t.Errorf("error.code = %q, want %q", code, "not_found")
	}
}

func TestBuildChain_ModuleRouteMissingPermissionReturns403(t *testing.T) {
	f := newChainFixture(t)
	h := f.chain(nil)
	token := f.issueToken(t)

	// /widgets/forbidden requires chainTestUngrantedPermission, which the
	// fixture's admin role was never granted — see that constant's own
	// doc comment for why this route exists instead of granting then
	// revoking a permission at runtime.
	req := httptest.NewRequest(http.MethodGet, "/widgets/forbidden", nil)
	req.Host = f.domain
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; body: %s", w.Code, w.Body.String())
	}
	if code := decodeErrorCode(t, w); code != "permission_denied" {
		t.Errorf("error.code = %q, want %q", code, "permission_denied")
	}
}

func TestBuildChain_EngineBuiltinRouteBypassesTenantAndAuthMiddleware(t *testing.T) {
	f := newChainFixture(t)
	var sawTenant, sawAuth bool
	builtins := map[string]http.Handler{
		"GET /_health": http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			sawTenant = tenantFromContext(r.Context()) != nil
			sawAuth = authFromContext(r.Context()) != nil
			w.WriteHeader(http.StatusOK)
		}),
	}
	h := f.chain(builtins)

	// A Host that would fail Class A tenant resolution — proving the
	// builtin route never even attempted it, since EngineBuiltin routes
	// are a deliberate no-op in tenantResolutionMiddleware/authMiddleware.
	// registerBuiltinRoutes (registry.go) sets EngineBuiltin: true on
	// /_health, which is what f.reg's route table (built via
	// registry.ModuleRegistry.Update -> buildRouteTable) carries here.
	req := httptest.NewRequest(http.MethodGet, "/_health", nil)
	req.Host = "no-such-tenant.example.com"
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", w.Code, w.Body.String())
	}
	if sawTenant {
		t.Error("tenantFromContext was populated for an EngineBuiltin route — tenantResolutionMiddleware should have been a no-op")
	}
	if sawAuth {
		t.Error("authFromContext was populated for an EngineBuiltin route — authMiddleware should have been a no-op")
	}
}

func TestBuildChain_MFARequiredPolicyUnenrolledUserReturns403SetupRequired(t *testing.T) {
	f := newChainFixture(t)
	ctx := context.Background()
	tenantConfigStore := tenantconfig.NewStore(f.conn)
	if err := tenantConfigStore.Set(ctx, f.tenantID, "mfa.enforcement_mode", string(enforce.ModeRequired)); err != nil {
		t.Fatalf("Set() error: %v", err)
	}

	h := f.chain(nil)
	token := f.issueToken(t)

	req := httptest.NewRequest(http.MethodGet, "/widgets/items", nil)
	req.Host = f.domain
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; body: %s", w.Code, w.Body.String())
	}
	if code := decodeErrorCode(t, w); code != "mfa_setup_required" {
		t.Errorf("error.code = %q, want %q", code, "mfa_setup_required")
	}
}

func TestRouteAuthMiddleware_OptionalAuthAllowsAnonymous(t *testing.T) {
	rr := &routeResolution{entry: &route.RouteEntry{Manifest: route.RouteManifest{Auth: "optional"}}}
	var called bool
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})
	h := routeAuthMiddleware()(next)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req = req.WithContext(withRouteResolution(req.Context(), rr))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if !called {
		t.Error("routeAuthMiddleware rejected an optional-auth route with no AuthContext at all")
	}
}

func TestRouteAuthMiddleware_RequiredAuthWithoutAuthContextReturns401(t *testing.T) {
	rr := &routeResolution{entry: &route.RouteEntry{Manifest: route.RouteManifest{Auth: "required"}}}
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("next handler called for a required-auth route with no AuthContext")
	})
	h := routeAuthMiddleware()(next)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req = req.WithContext(withRouteResolution(req.Context(), rr))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", w.Code)
	}
}

func TestRouteAuthMiddleware_RequiredAuthWithAuthenticatedContextPasses(t *testing.T) {
	rr := &routeResolution{entry: &route.RouteEntry{Manifest: route.RouteManifest{Auth: "required"}}}
	var called bool
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})
	h := routeAuthMiddleware()(next)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	ctx := withRouteResolution(req.Context(), rr)
	ctx = withAuthContext(ctx, &authcheck.AuthContext{IsAuthenticated: true})
	req = req.WithContext(ctx)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if !called {
		t.Error("routeAuthMiddleware rejected a required-auth route with an authenticated AuthContext")
	}
}

func TestRouteAuthMiddleware_EngineBuiltinRouteBypassesCheckEvenWithoutAuthContext(t *testing.T) {
	rr := &routeResolution{entry: &route.RouteEntry{Manifest: route.RouteManifest{Auth: "required", EngineBuiltin: true}}}
	var called bool
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})
	h := routeAuthMiddleware()(next)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req = req.WithContext(withRouteResolution(req.Context(), rr))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if !called {
		t.Error("routeAuthMiddleware should be a no-op for EngineBuiltin routes regardless of RouteManifest.Auth")
	}
}

// TestRouteAuthMiddleware_EngineNativeAloneDoesNotBypassCheck encodes
// goerp#369's fix: EngineNative (a dispatch-routing signal — see its doc
// comment on RouteManifest) and EngineBuiltin (an auth-bypass signal)
// used to be the same field. An EnableOps-derived Table/Transient CRUD
// route (route.RegisterModelRoutes) sets EngineNative: true but never
// EngineBuiltin — it must still enforce RouteManifest.Auth == "required"
// like any other module route, not be silently treated as a bypass
// route the way it would have been before this fix.
func TestRouteAuthMiddleware_EngineNativeAloneDoesNotBypassCheck(t *testing.T) {
	rr := &routeResolution{entry: &route.RouteEntry{Manifest: route.RouteManifest{Auth: "required", EngineNative: true}}}
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("next handler called for a required-auth EngineNative route with no AuthContext")
	})
	h := routeAuthMiddleware()(next)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req = req.WithContext(withRouteResolution(req.Context(), rr))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401 — EngineNative alone (without EngineBuiltin) must not bypass routeAuthMiddleware", w.Code)
	}
}
