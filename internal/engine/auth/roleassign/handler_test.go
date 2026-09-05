package roleassign

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
	"github.com/djangbahevans/goerp/internal/engine/route"
	"github.com/djangbahevans/goerp/internal/engine/secrets"
	"github.com/djangbahevans/goerp/internal/engine/tenant"
	tenantresolve "github.com/djangbahevans/goerp/internal/engine/tenant/resolve"
	"github.com/djangbahevans/goerp/internal/engine/tenantschema"
	"github.com/djangbahevans/goerp/internal/engine/user"
	"github.com/djangbahevans/goerp/internal/engine/ws"
)

const localPostgresDSN = "postgres://goerp:dev@localhost:55432/goerp"

// spyAudit records Emit calls instead of writing to a real audit log —
// same shape as mfareset's own spyAudit.
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
	roles      *role.Store
	sessions   *session.Store
	roleCache  *permcache.RoleCache
	revoker    *sessionrevoke.Revoker
	hub        *ws.Hub
	audit      *spyAudit
	domain     string
	tenantID   string
	tenantSlug string
	conn       *sql.DB
	cache      *cache.Client
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
	handler := NewHandler(tenantResolver, authChecker, roleStore, roleCache, sessionRevoker, hub, audit)

	slug := fmt.Sprintf("roleassigntest%d", time.Now().UnixNano())
	tt, err := tenantStore.CreateTenant(ctx, slug, "Role Assign Test Co")
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
		roles:      roleStore,
		sessions:   sessionStore,
		roleCache:  roleCache,
		revoker:    sessionRevoker,
		hub:        hub,
		audit:      audit,
		domain:     domain,
		tenantID:   tt.ID,
		tenantSlug: slug,
		conn:       conn,
		cache:      cacheClient,
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

// createUserWithRole creates an active user, grants roleName in the
// fixture's tenant (via a direct insert, ahead of AssignRole itself being
// under test), and registers cleanup.
func (f *fixture) createUserWithRole(t *testing.T, roleName string) (userID string) {
	t.Helper()
	ctx := t.Context()

	userStore := user.NewStore(f.conn)
	email := fmt.Sprintf("roleassigntest%d@example.com", time.Now().UnixNano())
	userID, err := userStore.FindOrCreateInvited(ctx, email)
	if err != nil {
		t.Fatalf("FindOrCreateInvited() error: %v", err)
	}
	t.Cleanup(func() { _, _ = f.conn.Exec(`DELETE FROM system.users WHERE id = $1`, userID) })
	if _, err := f.conn.Exec(`UPDATE system.users SET status = 'active' WHERE id = $1`, userID); err != nil {
		t.Fatalf("activate fixture user: %v", err)
	}
	t.Cleanup(func() { _, _ = f.conn.Exec(`DELETE FROM system.sessions WHERE user_id = $1`, userID) })

	roleID, err := f.roles.GetRoleByName(ctx, f.tenantSlug, roleName)
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

func (f *fixture) doAssign(t *testing.T, accessToken, targetID string, body map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	b, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal request body: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/admin/users/"+targetID+"/roles", bytes.NewReader(b))
	req.Host = f.domain
	req.RemoteAddr = "203.0.113.7:54321"
	if accessToken != "" {
		req.Header.Set("Authorization", "Bearer "+accessToken)
	}
	req = req.WithContext(route.WithParams(req.Context(), map[string]string{"id": targetID}))
	rec := httptest.NewRecorder()
	f.handler.ServeAssign(rec, req)
	return rec
}

func (f *fixture) doRevoke(t *testing.T, accessToken, targetID, roleName string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodDelete, "/admin/users/"+targetID+"/roles/"+roleName, nil)
	req.Host = f.domain
	req.RemoteAddr = "203.0.113.7:54321"
	if accessToken != "" {
		req.Header.Set("Authorization", "Bearer "+accessToken)
	}
	req = req.WithContext(route.WithParams(req.Context(), map[string]string{"id": targetID, "role": roleName}))
	rec := httptest.NewRecorder()
	f.handler.ServeRevoke(rec, req)
	return rec
}

func TestServeAssign_GrantsRoleInvalidatesCacheAndAudits(t *testing.T) {
	f := newFixture(t)
	callerID := f.createUserWithRole(t, "admin")
	callerToken := f.issueAccessToken(t, callerID)
	targetID := f.createUserWithRole(t, "user")

	// Prime the role cache and mark it as the session's baseline, so the
	// test can tell whether AssignRole's invalidation actually ran.
	f.roleCache.Set(t.Context(), f.tenantID, targetID, []string{"stale-entry"})
	targetTokens, err := f.issuer.Issue(t.Context(), authtoken.LoginParams{UserID: targetID, TenantSlug: f.tenantSlug, DeviceID: uuid.New().String()})
	if err != nil {
		t.Fatalf("issue target session: %v", err)
	}
	_ = targetTokens

	rec := f.doAssign(t, callerToken, targetID, map[string]any{"role": "admin"})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}

	names, err := f.roles.RoleNamesForUser(t.Context(), f.tenantSlug, targetID)
	if err != nil {
		t.Fatalf("RoleNamesForUser() error: %v", err)
	}
	found := false
	for _, n := range names {
		if n == "admin" {
			found = true
		}
	}
	if !found {
		t.Errorf("RoleNamesForUser() = %v, want it to contain %q", names, "admin")
	}

	if _, cacheHit := f.roleCache.Get(t.Context(), f.tenantID, targetID); cacheHit {
		t.Error("role cache still has an entry after AssignRole, want it invalidated")
	}

	targetSessions, err := f.sessions.NonRevokedIDsForUserInTenant(t.Context(), targetID, f.tenantID)
	if err != nil {
		t.Fatalf("NonRevokedIDsForUserInTenant() error: %v", err)
	}
	if len(targetSessions) == 0 {
		t.Fatal("target has no active sessions to check staleness for")
	}
	for _, sid := range targetSessions {
		stale, err := f.revoker.IsRolesStale(t.Context(), sid)
		if err != nil {
			t.Fatalf("IsRolesStale() error: %v", err)
		}
		if !stale {
			t.Errorf("session %s: IsRolesStale() = false, want true", sid)
		}
	}

	if !f.audit.called() {
		t.Error("audit emitter was never called")
	}
}

func TestServeAssign_IdempotentOnAlreadyGrantedRole(t *testing.T) {
	f := newFixture(t)
	callerID := f.createUserWithRole(t, "admin")
	callerToken := f.issueAccessToken(t, callerID)
	targetID := f.createUserWithRole(t, "user")

	rec1 := f.doAssign(t, callerToken, targetID, map[string]any{"role": "admin"})
	if rec1.Code != http.StatusOK {
		t.Fatalf("first assign status = %d, want 200", rec1.Code)
	}
	rec2 := f.doAssign(t, callerToken, targetID, map[string]any{"role": "admin"})
	if rec2.Code != http.StatusOK {
		t.Fatalf("second assign (already granted) status = %d, want 200", rec2.Code)
	}
}

func TestServeAssign_UnknownRoleReturns404(t *testing.T) {
	f := newFixture(t)
	callerID := f.createUserWithRole(t, "admin")
	callerToken := f.issueAccessToken(t, callerID)
	targetID := f.createUserWithRole(t, "user")

	rec := f.doAssign(t, callerToken, targetID, map[string]any{"role": "does-not-exist"})
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404; body = %s", rec.Code, rec.Body.String())
	}
}

func TestServeAssign_NonAdminCallerRejected(t *testing.T) {
	f := newFixture(t)
	callerID := f.createUserWithRole(t, "user")
	callerToken := f.issueAccessToken(t, callerID)
	targetID := f.createUserWithRole(t, "user")

	rec := f.doAssign(t, callerToken, targetID, map[string]any{"role": "admin"})
	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403; body = %s", rec.Code, rec.Body.String())
	}
}

func TestServeAssign_UnauthenticatedRejected(t *testing.T) {
	f := newFixture(t)
	targetID := f.createUserWithRole(t, "user")

	rec := f.doAssign(t, "", targetID, map[string]any{"role": "admin"})
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401; body = %s", rec.Code, rec.Body.String())
	}
}

func TestServeAssign_NonMemberTargetReturns404(t *testing.T) {
	f := newFixture(t)
	callerID := f.createUserWithRole(t, "admin")
	callerToken := f.issueAccessToken(t, callerID)

	userStore := user.NewStore(f.conn)
	outsiderID, err := userStore.FindOrCreateInvited(t.Context(), fmt.Sprintf("outsider%d@example.com", time.Now().UnixNano()))
	if err != nil {
		t.Fatalf("FindOrCreateInvited() error: %v", err)
	}
	t.Cleanup(func() { _, _ = f.conn.Exec(`DELETE FROM system.users WHERE id = $1`, outsiderID) })

	rec := f.doAssign(t, callerToken, outsiderID, map[string]any{"role": "admin"})
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404; body = %s", rec.Code, rec.Body.String())
	}
}

func TestServeRevoke_RevokesRoleAndInvalidatesCache(t *testing.T) {
	f := newFixture(t)
	callerID := f.createUserWithRole(t, "admin")
	callerToken := f.issueAccessToken(t, callerID)
	targetID := f.createUserWithRole(t, "user")
	if _, err := f.issuer.Issue(t.Context(), authtoken.LoginParams{UserID: targetID, TenantSlug: f.tenantSlug, DeviceID: uuid.New().String()}); err != nil {
		t.Fatalf("issue target session: %v", err)
	}

	rec := f.doRevoke(t, callerToken, targetID, "user")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}

	isMember, err := f.roles.IsMember(t.Context(), f.tenantSlug, targetID)
	if err != nil {
		t.Fatalf("IsMember() error: %v", err)
	}
	if isMember {
		t.Error("IsMember() = true after revoking the target's only role, want false")
	}

	targetSessions, err := f.sessions.NonRevokedIDsForUserInTenant(t.Context(), targetID, f.tenantID)
	if err != nil {
		t.Fatalf("NonRevokedIDsForUserInTenant() error: %v", err)
	}
	for _, sid := range targetSessions {
		stale, err := f.revoker.IsRolesStale(t.Context(), sid)
		if err != nil {
			t.Fatalf("IsRolesStale() error: %v", err)
		}
		if !stale {
			t.Errorf("session %s: IsRolesStale() = false, want true", sid)
		}
	}
}

func TestServeRevoke_UngrantedRoleIsANoOp(t *testing.T) {
	f := newFixture(t)
	callerID := f.createUserWithRole(t, "admin")
	callerToken := f.issueAccessToken(t, callerID)
	targetID := f.createUserWithRole(t, "user")

	rec := f.doRevoke(t, callerToken, targetID, "portal")
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200 (revoking an ungranted role is a no-op); body = %s", rec.Code, rec.Body.String())
	}
}

func TestServeRevoke_UnknownRoleReturns404(t *testing.T) {
	f := newFixture(t)
	callerID := f.createUserWithRole(t, "admin")
	callerToken := f.issueAccessToken(t, callerID)
	targetID := f.createUserWithRole(t, "user")

	rec := f.doRevoke(t, callerToken, targetID, "does-not-exist")
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404; body = %s", rec.Code, rec.Body.String())
	}
}

// connectUserConn dials a real WebSocket connection registered with hub
// under userID, subscribes it to UserChannel(userID), and returns the
// client side for reading broadcasts sent to that channel — mirrors
// moduleinstall's own connectTenantConn (goerp#621), adapted to a
// per-user channel.
func connectUserConn(t *testing.T, hub *ws.Hub, userID string) *websocket.Conn {
	t.Helper()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer func() { _ = conn.CloseNow() }()
		_ = hub.Serve(r.Context(), conn, uuid.New().String(), userID, "tenant-irrelevant", "test-agent")
	}))
	t.Cleanup(srv.Close)

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, "ws://"+srv.Listener.Addr().String(), nil)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	t.Cleanup(func() { _ = conn.CloseNow() })

	if err := wsjson.Write(ctx, conn, map[string]string{"type": "subscribe", "channel": ws.UserChannel(userID)}); err != nil {
		t.Fatalf("subscribe write: %v", err)
	}
	return conn
}

// waitForSubscription blocks until hub has actually registered conn's
// subscription to channel — Hub.Serve's read loop processes the client's
// "subscribe" frame asynchronously relative to the client-side write
// completing, so a broadcast fired immediately after connectUserConn
// returns can otherwise race the subscription landing (this package can't
// poll hub's own unexported subscriber map the way ws's own tests do, so
// it confirms delivery the same way dispatch_ws_test.go's
// TestDispatchWSRoute_AuthenticatedRequestUpgradesAndRegistersWithHub
// does: retry a real Broadcast on the channel until it actually reaches
// this connection).
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

func TestServeAssign_BroadcastsRoleChangedToTargetUserChannel(t *testing.T) {
	f := newFixture(t)
	callerID := f.createUserWithRole(t, "admin")
	callerToken := f.issueAccessToken(t, callerID)
	targetID := f.createUserWithRole(t, "user")

	conn := connectUserConn(t, f.hub, targetID)
	waitForSubscription(t, f.hub, ws.UserChannel(targetID), conn)

	rec := f.doAssign(t, callerToken, targetID, map[string]any{"role": "admin"})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}

	readCtx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	var env map[string]any
	if err := wsjson.Read(readCtx, conn, &env); err != nil {
		t.Fatalf("read broadcast envelope: %v", err)
	}
	if env["channel"] != ws.UserChannel(targetID) {
		t.Errorf("channel = %v, want %q", env["channel"], ws.UserChannel(targetID))
	}
	if env["type"] != "role.changed" {
		t.Errorf("type = %v, want %q", env["type"], "role.changed")
	}
}
