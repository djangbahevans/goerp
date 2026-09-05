package planchange

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
	"uuid"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"

	"github.com/djangbahevans/goerp/internal/engine/apikey"
	"github.com/djangbahevans/goerp/internal/engine/auth/authcheck"
	"github.com/djangbahevans/goerp/internal/engine/auth/authtoken"
	"github.com/djangbahevans/goerp/internal/engine/auth/session"
	"github.com/djangbahevans/goerp/internal/engine/auth/sessionrevoke"
	"github.com/djangbahevans/goerp/internal/engine/auth/signingkey"
	"github.com/djangbahevans/goerp/internal/engine/billing"
	"github.com/djangbahevans/goerp/internal/engine/cache"
	"github.com/djangbahevans/goerp/internal/engine/db"
	"github.com/djangbahevans/goerp/internal/engine/permcache"
	"github.com/djangbahevans/goerp/internal/engine/permission"
	"github.com/djangbahevans/goerp/internal/engine/role"
	"github.com/djangbahevans/goerp/internal/engine/secrets"
	"github.com/djangbahevans/goerp/internal/engine/tenant"
	tenantresolve "github.com/djangbahevans/goerp/internal/engine/tenant/resolve"
	"github.com/djangbahevans/goerp/internal/engine/tenantschema"
	"github.com/djangbahevans/goerp/internal/engine/user"
	"github.com/djangbahevans/goerp/internal/engine/ws"
)

const localPostgresDSN = "postgres://goerp:dev@localhost:55432/goerp"

// spyAudit records Emit calls instead of writing to a real audit log —
// same shape as roleassign's own spyAudit.
type spyAudit struct {
	mu     sync.Mutex
	events []map[string]any
}

func (a *spyAudit) Emit(ctx context.Context, tenantSlug, eventName string, payload map[string]any) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.events = append(a.events, map[string]any{"tenant": tenantSlug, "event": eventName, "payload": payload})
	return nil
}

func (a *spyAudit) called() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.events) > 0
}

type fixture struct {
	handler    *Handler
	issuer     *authtoken.Issuer
	resolver   *tenantresolve.Resolver
	tenants    *tenant.Store
	billing    *billing.Store
	hub        *ws.Hub
	audit      *spyAudit
	cache      *cache.Client
	domain     string
	tenantID   string
	tenantSlug string
	conn       *sql.DB
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	ctx := t.Context()

	conn, err := db.New(localPostgresDSN)
	if err != nil {
		t.Skipf("postgres not reachable at %s (start compose.dev.yml): %v", localPostgresDSN, err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	lockSharedKeyTable(t, conn)

	cacheClient, err := cache.New(ctx, cache.Config{Addr: "localhost:6379", DB: 0, MaxRetries: 1})
	if err != nil {
		t.Skipf("redis not reachable at localhost:6379 (start compose.dev.yml): %v", err)
	}
	t.Cleanup(func() { _ = cacheClient.Close() })

	tenantStore := tenant.NewStore(conn)
	if err := tenantStore.Bootstrap(ctx); err != nil {
		t.Fatalf("tenant Bootstrap() error: %v", err)
	}
	userStore := user.NewStore(conn)
	if err := userStore.Bootstrap(ctx); err != nil {
		t.Fatalf("user Bootstrap() error: %v", err)
	}
	sessionStore := session.NewStore(conn)
	if err := sessionStore.Bootstrap(ctx); err != nil {
		t.Fatalf("session Bootstrap() error: %v", err)
	}
	roleStore := role.NewStore(conn)
	apiKeys := apikey.NewStore(conn)
	if err := apiKeys.Bootstrap(ctx); err != nil {
		t.Fatalf("apikey Bootstrap() error: %v", err)
	}
	billingStore := billing.NewStore(conn)
	if err := billingStore.Bootstrap(ctx); err != nil {
		t.Fatalf("billing Bootstrap() error: %v", err)
	}

	signingKeyStore := signingkey.NewStore(conn, &secrets.EnvBackend{})
	if err := signingKeyStore.Bootstrap(ctx); err != nil {
		t.Fatalf("signingkey Bootstrap() error: %v", err)
	}
	signingKeySet, err := signingKeyStore.LoadOrGenerate(ctx)
	if err != nil {
		t.Fatalf("signingkey LoadOrGenerate() error: %v", err)
	}

	tenantResolver := tenantresolve.NewResolver(tenantStore, cacheClient, billingStore)
	issuer := authtoken.NewIssuer(&signingKeySet.Active, tenantStore, roleStore, sessionStore)
	roleCache := permcache.NewRoleCache(cacheClient)
	roleMap := permcache.NewRolePermissionMap()
	registry := permission.NewPermissionRegistry()
	sessionRevoker := sessionrevoke.NewRevoker(sessionStore, cacheClient)
	authChecker := authcheck.NewChecker(&signingKeySet.Active, sessionRevoker, userStore, roleStore, roleCache, roleMap, apiKeys, false, nil, nil, nil)
	hub := ws.NewHub()
	audit := &spyAudit{}
	handler := NewHandler(tenantResolver, authChecker, billingStore, tenantStore, cacheClient, hub, audit)

	slug := fmt.Sprintf("planchangetest%d", time.Now().UnixNano())
	tt, err := tenantStore.CreateTenant(ctx, slug, "Plan Change Test Co")
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

	schema := tenantschema.Name(slug)
	if _, err := conn.Exec("CREATE SCHEMA " + schema); err != nil {
		t.Fatalf("create fixture schema: %v", err)
	}
	t.Cleanup(func() { _, _ = conn.Exec(fmt.Sprintf("DROP SCHEMA %s CASCADE", schema)) })
	if err := roleStore.Bootstrap(ctx, slug); err != nil {
		t.Fatalf("role Bootstrap() error: %v", err)
	}
	if err := roleStore.SeedBuiltinRoles(ctx, slug); err != nil {
		t.Fatalf("SeedBuiltinRoles() error: %v", err)
	}
	if err := roleMap.RebuildAll(ctx, tenantStore, roleStore, registry); err != nil {
		t.Fatalf("RebuildAll() error: %v", err)
	}

	return &fixture{
		handler:    handler,
		issuer:     issuer,
		resolver:   tenantResolver,
		tenants:    tenantStore,
		billing:    billingStore,
		hub:        hub,
		audit:      audit,
		cache:      cacheClient,
		domain:     domain,
		tenantID:   tt.ID,
		tenantSlug: slug,
		conn:       conn,
	}
}

func lockSharedKeyTable(t *testing.T, pool *sql.DB) {
	t.Helper()
	ctx := t.Context()
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
}

// createUserWithRole creates an active user and grants roleName in the
// fixture's tenant, mirroring roleassign's own helper.
func (f *fixture) createUserWithRole(t *testing.T, roleName string) (userID string) {
	t.Helper()
	ctx := t.Context()

	userStore := user.NewStore(f.conn)
	email := fmt.Sprintf("planchangetest%d@example.com", time.Now().UnixNano())
	userID, err := userStore.FindOrCreateInvited(ctx, email)
	if err != nil {
		t.Fatalf("FindOrCreateInvited() error: %v", err)
	}
	t.Cleanup(func() { _, _ = f.conn.Exec(`DELETE FROM system.users WHERE id = $1`, userID) })
	if _, err := f.conn.Exec(`UPDATE system.users SET status = 'active' WHERE id = $1`, userID); err != nil {
		t.Fatalf("activate fixture user: %v", err)
	}
	t.Cleanup(func() { _, _ = f.conn.Exec(`DELETE FROM system.sessions WHERE user_id = $1`, userID) })

	roleStore := role.NewStore(f.conn)
	roleID, err := roleStore.GetRoleByName(ctx, f.tenantSlug, roleName)
	if err != nil {
		t.Fatalf("GetRoleByName(%q) error: %v", roleName, err)
	}
	schema := tenantschema.Name(f.tenantSlug)
	if _, err := f.conn.Exec(fmt.Sprintf("INSERT INTO %s.user_roles (user_id, role_id) VALUES ($1, $2)", schema), userID, roleID); err != nil {
		t.Fatalf("grant role %q: %v", roleName, err)
	}

	return userID
}

func (f *fixture) issueAccessToken(t *testing.T, userID string) string {
	t.Helper()
	tokens, err := f.issuer.Issue(t.Context(), authtoken.LoginParams{
		UserID:     userID,
		TenantSlug: f.tenantSlug,
		DeviceID:   uuid.New().String(),
	})
	if err != nil {
		t.Fatalf("Issue() error: %v", err)
	}
	return tokens.AccessToken
}

// createPlan creates a billing plan named literally for one of the four
// tenant.Plan enum values — the endpoint requires GetPlanByName to
// resolve against exactly that name (see the handler's own doc comment
// on allowedPlans), so unlike billing's own uniqueName-suffixed test
// plans, this one must use the bare enum string. system.plans.name is
// UNIQUE, so cleanup runs before the next test that creates the same
// name — safe since these tests never run in parallel.
func (f *fixture) createPlan(t *testing.T, name tenant.Plan) *billing.Plan {
	t.Helper()
	p, err := f.billing.CreatePlan(t.Context(), string(name), string(name), nil, nil)
	if err != nil {
		t.Fatalf("CreatePlan(%q) error: %v", name, err)
	}
	// t.Cleanup runs LIFO, so this (registered in the test body, after
	// newFixture's own tenant-delete cleanup) runs before that tenant
	// delete — plans.id has no ON DELETE CASCADE from tenant_subscriptions
	// (deliberately: multitenancy-internals.md §2 never expects a
	// plan referenced by a live subscription to be deleted), so deleting
	// the referencing subscription row first, unconditionally, keeps this
	// cleanup order-independent instead of silently failing and leaking
	// the plan row into the next test.
	t.Cleanup(func() {
		_, _ = f.conn.Exec("DELETE FROM system.tenant_subscriptions WHERE plan_id = $1", p.ID)
		_, _ = f.conn.Exec("DELETE FROM system.plans WHERE id = $1", p.ID)
	})
	return p
}

// createActiveSubscription creates a tenant_subscriptions row for the
// fixture tenant on planID and marks it active (CreateSubscription
// defaults to trialing).
func (f *fixture) createActiveSubscription(t *testing.T, planID string) {
	t.Helper()
	now := time.Now()
	sub, err := f.billing.CreateSubscription(t.Context(), f.tenantID, planID, now, now.Add(30*24*time.Hour))
	if err != nil {
		t.Fatalf("CreateSubscription() error: %v", err)
	}
	if _, err := f.conn.Exec("UPDATE system.tenant_subscriptions SET status = 'active' WHERE id = $1", sub.ID); err != nil {
		t.Fatalf("mark subscription active: %v", err)
	}
}

func (f *fixture) doChangePlan(t *testing.T, accessToken string, body map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	b, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal request body: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/admin/tenant/plan", bytes.NewReader(b))
	req.Host = f.domain
	req.RemoteAddr = "203.0.113.7:54321"
	if accessToken != "" {
		req.Header.Set("Authorization", "Bearer "+accessToken)
	}
	rec := httptest.NewRecorder()
	f.handler.ServeHTTP(rec, req)
	return rec
}

func TestServeHTTP_ChangesPlanInvalidatesCacheAndAudits(t *testing.T) {
	f := newFixture(t)
	callerID := f.createUserWithRole(t, "admin")
	callerToken := f.issueAccessToken(t, callerID)

	oldPlan := f.createPlan(t, tenant.PlanStarter)
	f.createActiveSubscription(t, oldPlan.ID)
	newPlan := f.createPlan(t, tenant.PlanPro)

	// Prime the entitlement cache so the test can tell whether the
	// handler's own invalidation actually ran.
	if _, err := f.resolver.LoadEntitlements(t.Context(), f.tenantID); err != nil {
		t.Fatalf("prime LoadEntitlements() error: %v", err)
	}
	if _, found, err := f.cache.Get(t.Context(), tenantresolve.EntitlementCacheKey(f.tenantID)); err != nil || !found {
		t.Fatalf("expected entitlement cache to be primed before the change; found=%v err=%v", found, err)
	}

	// Prime the domain cache the same way — ResolveByHost caches the full
	// tenant.Tenant (including the stale Plan) under DomainCacheKey.
	if _, err := f.resolver.ResolveByHost(t.Context(), f.domain); err != nil {
		t.Fatalf("prime ResolveByHost() error: %v", err)
	}
	if _, found, err := f.cache.Get(t.Context(), tenantresolve.DomainCacheKey(f.domain)); err != nil || !found {
		t.Fatalf("expected domain cache to be primed before the change; found=%v err=%v", found, err)
	}

	rec := f.doChangePlan(t, callerToken, map[string]any{"plan": string(tenant.PlanPro)})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}

	got, err := f.tenants.GetByID(t.Context(), f.tenantID)
	if err != nil {
		t.Fatalf("GetByID() error: %v", err)
	}
	if got.Plan != tenant.PlanPro {
		t.Errorf("tenants.plan = %q, want %q", got.Plan, tenant.PlanPro)
	}

	if _, found, err := f.cache.Get(t.Context(), tenantresolve.EntitlementCacheKey(f.tenantID)); err != nil || found {
		t.Errorf("expected entitlement cache to be invalidated after the change; found=%v err=%v", found, err)
	}
	if _, found, err := f.cache.Get(t.Context(), tenantresolve.DomainCacheKey(f.domain)); err != nil || found {
		t.Errorf("expected domain cache to be invalidated after the change; found=%v err=%v", found, err)
	}

	var planID string
	if err := f.conn.QueryRow("SELECT plan_id FROM system.tenant_subscriptions WHERE tenant_id = $1 AND status = 'active'", f.tenantID).Scan(&planID); err != nil {
		t.Fatalf("query subscription plan_id: %v", err)
	}
	if planID != newPlan.ID {
		t.Errorf("subscription plan_id = %q, want %q", planID, newPlan.ID)
	}

	if !f.audit.called() {
		t.Error("audit emitter was never called")
	}
}

func TestServeHTTP_UnknownPlanNameReturns400(t *testing.T) {
	f := newFixture(t)
	callerID := f.createUserWithRole(t, "admin")
	callerToken := f.issueAccessToken(t, callerID)

	rec := f.doChangePlan(t, callerToken, map[string]any{"plan": "not-a-real-tier"})
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400; body = %s", rec.Code, rec.Body.String())
	}
}

func TestServeHTTP_PlanNotInBillingCatalogReturns404(t *testing.T) {
	f := newFixture(t)
	callerID := f.createUserWithRole(t, "admin")
	callerToken := f.issueAccessToken(t, callerID)

	// "enterprise" is a valid tenant.Plan enum value, but no
	// system.plans row named "enterprise" exists in this fixture.
	rec := f.doChangePlan(t, callerToken, map[string]any{"plan": string(tenant.PlanEnterprise)})
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404; body = %s", rec.Code, rec.Body.String())
	}
}

func TestServeHTTP_NoActiveSubscriptionReturns409(t *testing.T) {
	f := newFixture(t)
	callerID := f.createUserWithRole(t, "admin")
	callerToken := f.issueAccessToken(t, callerID)
	f.createPlan(t, tenant.PlanPro)

	rec := f.doChangePlan(t, callerToken, map[string]any{"plan": string(tenant.PlanPro)})
	if rec.Code != http.StatusConflict {
		t.Errorf("status = %d, want 409; body = %s", rec.Code, rec.Body.String())
	}
}

func TestServeHTTP_NonAdminCallerRejected(t *testing.T) {
	f := newFixture(t)
	callerID := f.createUserWithRole(t, "user")
	callerToken := f.issueAccessToken(t, callerID)

	rec := f.doChangePlan(t, callerToken, map[string]any{"plan": string(tenant.PlanPro)})
	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403; body = %s", rec.Code, rec.Body.String())
	}
}

func TestServeHTTP_UnauthenticatedRejected(t *testing.T) {
	f := newFixture(t)

	rec := f.doChangePlan(t, "", map[string]any{"plan": string(tenant.PlanPro)})
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401; body = %s", rec.Code, rec.Body.String())
	}
}

// connectTenantConn dials a real WebSocket connection registered with hub
// under tenantID, subscribes it to TenantChannel(tenantID), and returns
// the client side for reading broadcasts sent to that channel — mirrors
// moduleinstall's own connectTenantConn (goerp#621).
func connectTenantConn(t *testing.T, hub *ws.Hub, tenantID string) *websocket.Conn {
	t.Helper()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer func() { _ = conn.CloseNow() }()
		_ = hub.Serve(r.Context(), conn, uuid.New().String(), "user-1", tenantID, "test-agent")
	}))
	t.Cleanup(srv.Close)

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, "ws://"+srv.Listener.Addr().String(), nil)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	t.Cleanup(func() { _ = conn.CloseNow() })

	if err := wsjson.Write(ctx, conn, map[string]string{"type": "subscribe", "channel": ws.TenantChannel(tenantID)}); err != nil {
		t.Fatalf("subscribe write: %v", err)
	}
	return conn
}

// waitForSubscription blocks until hub has actually registered conn's
// subscription to channel — same canary-retry reasoning roleassign's own
// waitForSubscription documents (Hub.Serve's read loop processes the
// client's "subscribe" frame asynchronously relative to the client-side
// write completing).
func waitForSubscription(t *testing.T, hub *ws.Hub, channel string, conn *websocket.Conn) {
	t.Helper()

	deadline := time.Now().Add(5 * time.Second)
	for {
		reached, err := hub.Broadcast(t.Context(), channel, "test.canary", nil)
		if err != nil {
			t.Fatalf("canary Broadcast: %v", err)
		}
		if reached > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("subscription never registered with the hub in time")
		}
		time.Sleep(5 * time.Millisecond)
	}

	readCtx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	var canary map[string]any
	if err := wsjson.Read(readCtx, conn, &canary); err != nil {
		t.Fatalf("read canary envelope: %v", err)
	}
}

func TestServeHTTP_BroadcastsPlanChangedToTenantChannel(t *testing.T) {
	f := newFixture(t)
	callerID := f.createUserWithRole(t, "admin")
	callerToken := f.issueAccessToken(t, callerID)
	oldPlan := f.createPlan(t, tenant.PlanStarter)
	f.createActiveSubscription(t, oldPlan.ID)
	f.createPlan(t, tenant.PlanPro)

	conn := connectTenantConn(t, f.hub, f.tenantID)
	waitForSubscription(t, f.hub, ws.TenantChannel(f.tenantID), conn)

	rec := f.doChangePlan(t, callerToken, map[string]any{"plan": string(tenant.PlanPro)})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}

	readCtx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	var env map[string]any
	if err := wsjson.Read(readCtx, conn, &env); err != nil {
		t.Fatalf("read broadcast envelope: %v", err)
	}
	if env["channel"] != ws.TenantChannel(f.tenantID) {
		t.Errorf("channel = %v, want %q", env["channel"], ws.TenantChannel(f.tenantID))
	}
	if env["type"] != "plan.changed" {
		t.Errorf("type = %v, want %q", env["type"], "plan.changed")
	}
}
