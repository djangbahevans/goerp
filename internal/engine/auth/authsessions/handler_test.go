package authsessions

import (
	"context"
	"database/sql"
	"encoding/json/v2"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
	"time"
	"uuid"

	"github.com/djangbahevans/goerp/internal/engine/apikey"
	"github.com/djangbahevans/goerp/internal/engine/auth/authcheck"
	"github.com/djangbahevans/goerp/internal/engine/auth/authtoken"
	"github.com/djangbahevans/goerp/internal/engine/auth/session"
	"github.com/djangbahevans/goerp/internal/engine/auth/sessionrevoke"
	"github.com/djangbahevans/goerp/internal/engine/auth/signingkey"
	"github.com/djangbahevans/goerp/internal/engine/authaudit"
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
)

const localPostgresDSN = "postgres://goerp:dev@localhost:55432/goerp"

type env struct {
	conn     *sql.DB
	tenants  *tenant.Store
	users    *user.Store
	roles    *role.Store
	sessions *session.Store
	issuer   *authtoken.Issuer
	checker  *authcheck.Checker
	revoker  *sessionrevoke.Revoker
	roleMap  *permcache.RolePermissionMap
	handler  *Handler
}

type fixtureTenant struct {
	id     string
	slug   string
	domain string
}

// login is one issued session family.
type login struct {
	access  string
	refresh string
	device  string
	family  string
}

func newEnv(t *testing.T) *env {
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
	userStore := user.NewStore(conn)
	sessionStore := session.NewStore(conn)
	apiKeys := apikey.NewStore(conn)
	billingStore := billing.NewStore(conn)
	auditStore := authaudit.NewStore(conn, tenantStore)
	signingKeyStore := signingkey.NewStore(conn, &secrets.EnvBackend{})
	for name, bootstrap := range map[string]func(context.Context) error{
		"tenant": tenantStore.Bootstrap, "user": userStore.Bootstrap, "session": sessionStore.Bootstrap,
		"apikey": apiKeys.Bootstrap, "billing": billingStore.Bootstrap, "authaudit": auditStore.Bootstrap,
		"signingkey": signingKeyStore.Bootstrap,
	} {
		if err := bootstrap(ctx); err != nil {
			t.Fatalf("%s Bootstrap() error: %v", name, err)
		}
	}
	signingKeySet, err := signingKeyStore.LoadOrGenerate(ctx)
	if err != nil {
		t.Fatalf("signingkey LoadOrGenerate() error: %v", err)
	}

	roleStore := role.NewStore(conn)
	roleMap := permcache.NewRolePermissionMap()
	revoker := sessionrevoke.NewRevoker(sessionStore, cacheClient)
	checker := authcheck.NewChecker(&signingKeySet.Active, revoker, userStore, roleStore, permcache.NewRoleCache(cacheClient), roleMap, apiKeys, false, nil, nil, nil)
	resolver := tenantresolve.NewResolver(tenantStore, cacheClient, billingStore)

	return &env{
		conn:     conn,
		tenants:  tenantStore,
		users:    userStore,
		roles:    roleStore,
		sessions: sessionStore,
		issuer:   authtoken.NewIssuer(&signingKeySet.Active, tenantStore, roleStore, sessionStore),
		checker:  checker,
		revoker:  revoker,
		roleMap:  roleMap,
		handler:  NewHandler(resolver, checker, sessionStore, revoker, auditStore),
	}
}

// lockSharedKeyTable serializes against other packages' tests that
// generate or rotate the shared jwt_signing_keys row.
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

func (e *env) newTenant(t *testing.T) fixtureTenant {
	t.Helper()
	ctx := t.Context()

	slug := fmt.Sprintf("authsessionstest%d", time.Now().UnixNano())
	tt, err := e.tenants.CreateTenant(ctx, slug, "Auth Sessions Test Co")
	if err != nil {
		t.Fatalf("CreateTenant() error: %v", err)
	}
	t.Cleanup(func() {
		_, _ = e.conn.Exec(`DELETE FROM system.auth_audit_log WHERE tenant_id = $1`, tt.ID)
		_, _ = e.conn.Exec(`DELETE FROM system.sessions WHERE tenant_id = $1`, tt.ID)
		_, _ = e.conn.Exec(`DELETE FROM system.tenants WHERE id = $1`, tt.ID)
	})
	if _, err := e.tenants.UpdateStatus(ctx, slug, tenant.StatusActive, nil); err != nil {
		t.Fatalf("activate fixture tenant: %v", err)
	}
	domain := slug + ".goerp.test"
	if _, err := e.tenants.CreateDomain(ctx, tt.ID, domain, tenant.DomainSubdomain, true); err != nil {
		t.Fatalf("CreateDomain() error: %v", err)
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
	if err := e.roleMap.RebuildAll(ctx, e.tenants, e.roles, permission.NewPermissionRegistry()); err != nil {
		t.Fatalf("RebuildAll() error: %v", err)
	}
	return fixtureTenant{id: tt.ID, slug: slug, domain: domain}
}

// newUser creates an active user and registers cleanup.
func (e *env) newUser(t *testing.T) string {
	t.Helper()
	id, err := e.users.FindOrCreateInvited(t.Context(), fmt.Sprintf("sessions%d@example.com", time.Now().UnixNano()))
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
	return id
}

func (e *env) join(t *testing.T, ft fixtureTenant, userID string) {
	t.Helper()
	roleID, err := e.roles.GetRoleByName(t.Context(), ft.slug, "user")
	if err != nil {
		t.Fatalf("GetRoleByName() error: %v", err)
	}
	if err := e.roles.AssignRole(t.Context(), ft.slug, userID, roleID, ""); err != nil {
		t.Fatalf("AssignRole() error: %v", err)
	}
}

func (e *env) login(t *testing.T, ft fixtureTenant, userID string) login {
	t.Helper()
	device := uuid.New().String()
	tokens, err := e.issuer.Issue(t.Context(), authtoken.LoginParams{UserID: userID, TenantSlug: ft.slug, DeviceID: device})
	if err != nil {
		t.Fatalf("Issue() error: %v", err)
	}
	authCtx, err := e.checker.Authenticate(t.Context(), tokens.AccessToken, ft.id, ft.slug, "203.0.113.7", nil, nil)
	if err != nil {
		t.Fatalf("Authenticate() error: %v", err)
	}
	family, err := e.sessions.FamilyIDForSession(t.Context(), authCtx.SessionID)
	if err != nil {
		t.Fatalf("FamilyIDForSession() error: %v", err)
	}
	return login{access: tokens.AccessToken, refresh: tokens.RefreshToken, device: device, family: family}
}

// refresh rotates l's refresh token, returning the new login and whether
// the rotation succeeded.
func (e *env) refresh(t *testing.T, l login) (login, bool) {
	t.Helper()
	tokens, outcome, err := e.issuer.Refresh(t.Context(), l.refresh, authtoken.RefreshParams{DeviceID: l.device})
	if err != nil {
		t.Fatalf("Refresh() error: %v", err)
	}
	if outcome != session.RotateOK {
		return login{}, false
	}
	return login{access: tokens.AccessToken, refresh: tokens.RefreshToken, device: l.device, family: l.family}, true
}

func (e *env) authenticates(t *testing.T, ft fixtureTenant, token string) bool {
	t.Helper()
	authCtx, err := e.checker.Authenticate(t.Context(), token, ft.id, ft.slug, "203.0.113.7", nil, nil)
	return err == nil && authCtx.IsAuthenticated
}

func (e *env) auditCount(t *testing.T, ft fixtureTenant, userID string) int {
	t.Helper()
	var n int
	if err := e.conn.QueryRow(`SELECT COUNT(*) FROM system.auth_audit_log WHERE tenant_id = $1 AND event_type = 'session.revoked' AND user_id = $2`, ft.id, userID).Scan(&n); err != nil {
		t.Fatalf("count audit rows: %v", err)
	}
	return n
}

func do(ft fixtureTenant, token string, serve func(http.ResponseWriter, *http.Request), method, path string, params map[string]string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, nil)
	r.Host = ft.domain
	r.RemoteAddr = "203.0.113.7:54321"
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	r = r.WithContext(route.WithParams(r.Context(), params))
	rec := httptest.NewRecorder()
	serve(rec, r)
	return rec
}

func (e *env) list(t *testing.T, ft fixtureTenant, token string) []sessionJSON {
	t.Helper()
	rec := do(ft, token, e.handler.ServeList, http.MethodGet, "/auth/sessions", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /auth/sessions: status = %d, body = %s", rec.Code, rec.Body)
	}
	var body struct {
		Sessions []sessionJSON `json:"sessions"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode sessions: %v", err)
	}
	return body.Sessions
}

func (e *env) revoke(ft fixtureTenant, token, familyID string) *httptest.ResponseRecorder {
	return do(ft, token, e.handler.ServeRevoke, http.MethodDelete, "/auth/sessions/"+familyID, map[string]string{"family_id": familyID})
}

func TestServeList_OneEntryPerFamilyCurrentFirst(t *testing.T) {
	e := newEnv(t)
	ft := e.newTenant(t)
	caller := e.newUser(t)
	e.join(t, ft, caller)

	rotated := e.login(t, ft, caller)
	for range 3 {
		var ok bool
		if rotated, ok = e.refresh(t, rotated); !ok {
			t.Fatal("refresh failed")
		}
	}
	other := e.login(t, ft, caller)
	current := e.login(t, ft, caller)
	if _, err := e.conn.Exec(`UPDATE system.sessions SET last_active_at = NOW() + INTERVAL '1 minute' WHERE family_id = $1 AND rotated_at IS NULL`, rotated.family); err != nil {
		t.Fatalf("bump last_active_at: %v", err)
	}

	got := e.list(t, ft, current.access)
	ids := make([]string, len(got))
	for i, s := range got {
		ids[i] = s.ID
		if s.Current != (s.ID == current.family) {
			t.Errorf("session %s current = %v", s.ID, s.Current)
		}
	}
	if want := []string{current.family, rotated.family, other.family}; !slices.Equal(ids, want) {
		t.Fatalf("sessions = %v, want %v (current, then most recently active)", ids, want)
	}

	var firstCreated time.Time
	if err := e.conn.QueryRow(`SELECT MIN(created_at) FROM system.sessions WHERE family_id = $1`, rotated.family).Scan(&firstCreated); err != nil {
		t.Fatalf("read family start: %v", err)
	}
	if !got[1].SignedInAt.Equal(firstCreated) {
		t.Errorf("rotated family signed_in_at = %v, want %v (the family's first row)", got[1].SignedInAt, firstCreated)
	}
}

func TestServeList_OnlyTheCallersSessionsInThisTenant(t *testing.T) {
	e := newEnv(t)
	ft := e.newTenant(t)
	elsewhere := e.newTenant(t)
	caller := e.newUser(t)
	bystander := e.newUser(t)
	e.join(t, ft, caller)
	e.join(t, elsewhere, caller)
	e.join(t, ft, bystander)

	mine := e.login(t, ft, caller)
	otherTenant := e.login(t, elsewhere, caller)
	theirs := e.login(t, ft, bystander)

	got := e.list(t, ft, mine.access)
	if len(got) != 1 || got[0].ID != mine.family {
		t.Fatalf("sessions = %+v, want only %s", got, mine.family)
	}
	for _, family := range []string{otherTenant.family, theirs.family, uuid.New().String(), "not-a-uuid"} {
		if rec := e.revoke(ft, mine.access, family); rec.Code != http.StatusNotFound {
			t.Errorf("revoke %s: status = %d, want 404", family, rec.Code)
		}
	}
	if !e.authenticates(t, elsewhere, otherTenant.access) || !e.authenticates(t, ft, theirs.access) {
		t.Error("a session outside the caller's reach was revoked")
	}
}

func TestServeRevoke_EndsTheFamilyButNotTheCurrentSession(t *testing.T) {
	e := newEnv(t)
	ft := e.newTenant(t)
	caller := e.newUser(t)
	e.join(t, ft, caller)
	target := e.login(t, ft, caller)
	current := e.login(t, ft, caller)

	if rec := e.revoke(ft, current.access, current.family); rec.Code != http.StatusBadRequest {
		t.Errorf("revoke current: status = %d, want 400", rec.Code)
	}

	if rec := e.revoke(ft, current.access, target.family); rec.Code != http.StatusNoContent {
		t.Fatalf("revoke: status = %d, body = %s", rec.Code, rec.Body)
	}
	if e.authenticates(t, ft, target.access) {
		t.Error("revoked session's access token still authenticates")
	}
	if _, ok := e.refresh(t, target); ok {
		t.Error("revoked session's refresh token still rotates")
	}
	if !e.authenticates(t, ft, current.access) {
		t.Error("current session stopped authenticating")
	}
	if n := e.auditCount(t, ft, caller); n != 1 {
		t.Errorf("session.revoked rows = %d, want 1", n)
	}
	if rec := e.revoke(ft, current.access, target.family); rec.Code != http.StatusNotFound {
		t.Errorf("revoking an already-revoked family: status = %d, want 404", rec.Code)
	}
}

func TestServeRevokeOthers_KeepsTheCallerAndThisTenantOnly(t *testing.T) {
	e := newEnv(t)
	ft := e.newTenant(t)
	elsewhere := e.newTenant(t)
	caller := e.newUser(t)
	e.join(t, ft, caller)
	e.join(t, elsewhere, caller)

	first := e.login(t, ft, caller)
	rotated := e.login(t, ft, caller)
	if _, ok := e.refresh(t, rotated); !ok {
		t.Fatal("refresh failed")
	}
	otherTenant := e.login(t, elsewhere, caller)
	current := e.login(t, ft, caller)
	current, ok := e.refresh(t, current)
	if !ok {
		t.Fatal("refresh of the current session failed")
	}

	rec := do(ft, current.access, e.handler.ServeRevokeOthers, http.MethodDelete, "/auth/sessions", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("DELETE /auth/sessions: status = %d, body = %s", rec.Code, rec.Body)
	}
	var body struct {
		Revoked int `json:"revoked"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Revoked != 2 {
		t.Errorf("revoked = %d, want 2", body.Revoked)
	}

	if e.authenticates(t, ft, first.access) {
		t.Error("another session in this tenant still authenticates")
	}
	if !e.authenticates(t, ft, current.access) {
		t.Error("the caller's session stopped authenticating")
	}
	if !e.authenticates(t, elsewhere, otherTenant.access) {
		t.Error("the caller's session in another tenant was revoked")
	}
	if got := e.list(t, ft, current.access); len(got) != 1 || got[0].ID != current.family {
		t.Errorf("sessions after revoking others = %+v, want only the current family", got)
	}
	if n := e.auditCount(t, ft, caller); n != 2 {
		t.Errorf("session.revoked rows = %d, want 2", n)
	}
}

func TestEveryRoute_RequiresAnAccessToken(t *testing.T) {
	e := newEnv(t)
	ft := e.newTenant(t)
	family := uuid.New().String()
	routes := map[string]*httptest.ResponseRecorder{
		"GET":        do(ft, "", e.handler.ServeList, http.MethodGet, "/auth/sessions", nil),
		"DELETE one": do(ft, "", e.handler.ServeRevoke, http.MethodDelete, "/auth/sessions/"+family, map[string]string{"family_id": family}),
		"DELETE all": do(ft, "", e.handler.ServeRevokeOthers, http.MethodDelete, "/auth/sessions", nil),
		"bad token":  do(ft, "not-a-token", e.handler.ServeList, http.MethodGet, "/auth/sessions", nil),
	}
	for name, rec := range routes {
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("%s: status = %d, want 401", name, rec.Code)
		}
	}
}
