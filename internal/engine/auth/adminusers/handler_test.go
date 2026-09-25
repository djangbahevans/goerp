package adminusers

import (
	"bytes"
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
	"github.com/djangbahevans/goerp/internal/engine/invite"
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

// env holds the stores shared by every fixture tenant in one test.
type env struct {
	conn     *sql.DB
	tenants  *tenant.Store
	users    *user.Store
	roles    *role.Store
	invites  *invite.Store
	sessions *session.Store
	issuer   *authtoken.Issuer
	checker  *authcheck.Checker
	handler  *Handler
	roleMap  *permcache.RolePermissionMap
	mailer   *spyMailer
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
	mailer := &spyMailer{}
	inviteStore := invite.NewStore(conn, userStore, roleStore, auditStore, mailer)

	return &env{
		conn:     conn,
		tenants:  tenantStore,
		users:    userStore,
		roles:    roleStore,
		invites:  inviteStore,
		sessions: sessionStore,
		issuer:   authtoken.NewIssuer(&signingKeySet.Active, tenantStore, roleStore, sessionStore),
		checker:  checker,
		handler:  NewHandler(resolver, checker, NewStore(conn, auditStore), roleStore, sessionStore, revoker, inviteStore, userStore, nil, nil),
		roleMap:  roleMap,
		mailer:   mailer,
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

func (e *env) newTenant(t *testing.T) fixtureTenant {
	t.Helper()
	ctx := t.Context()

	slug := fmt.Sprintf("adminuserstest%d", time.Now().UnixNano())
	tt, err := e.tenants.CreateTenant(ctx, slug, "Admin Users Test Co")
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
	if err := e.invites.Bootstrap(ctx, slug); err != nil {
		t.Fatalf("invite Bootstrap() error: %v", err)
	}
	if err := e.roleMap.RebuildAll(ctx, e.tenants, e.roles, permission.NewPermissionRegistry()); err != nil {
		t.Fatalf("RebuildAll() error: %v", err)
	}
	return fixtureTenant{id: tt.ID, slug: slug, domain: domain}
}

// createUser creates an active user with a profile named name and
// registers cleanup.
func (e *env) createUser(t *testing.T, email, name string) string {
	t.Helper()
	ctx := t.Context()
	id, err := e.users.FindOrCreateInvited(ctx, email)
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
	if err := e.users.EnsureProfile(ctx, id, name); err != nil {
		t.Fatalf("EnsureProfile() error: %v", err)
	}
	return id
}

func (e *env) grant(t *testing.T, ft fixtureTenant, userID, roleName string) {
	t.Helper()
	roleID, err := e.roles.GetRoleByName(t.Context(), ft.slug, roleName)
	if err != nil {
		t.Fatalf("GetRoleByName(%q) error: %v", roleName, err)
	}
	if err := e.roles.AssignRole(t.Context(), ft.slug, userID, roleID, ""); err != nil {
		t.Fatalf("AssignRole() error: %v", err)
	}
}

func (e *env) member(t *testing.T, ft fixtureTenant, prefix, name, roleName string) string {
	t.Helper()
	id := e.createUser(t, fmt.Sprintf("%s%d@example.com", prefix, time.Now().UnixNano()), name)
	e.grant(t, ft, id, roleName)
	return id
}

func (e *env) issue(t *testing.T, ft fixtureTenant, userID string) string {
	t.Helper()
	tokens, err := e.issuer.Issue(t.Context(), authtoken.LoginParams{UserID: userID, TenantSlug: ft.slug, DeviceID: uuid.New().String()})
	if err != nil {
		t.Fatalf("Issue() error: %v", err)
	}
	return tokens.AccessToken
}

func (e *env) authenticates(t *testing.T, ft fixtureTenant, token string) bool {
	t.Helper()
	authCtx, err := e.checker.Authenticate(t.Context(), token, ft.id, ft.slug, "203.0.113.7", nil, nil)
	return err == nil && authCtx.IsAuthenticated
}

func (e *env) familyIDs(t *testing.T, userID string) []string {
	t.Helper()
	rows, err := e.conn.Query(`SELECT family_id FROM system.sessions WHERE user_id = $1 ORDER BY created_at`, userID)
	if err != nil {
		t.Fatalf("query families: %v", err)
	}
	defer func() { _ = rows.Close() }()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatalf("scan family: %v", err)
		}
		ids = append(ids, id)
	}
	return ids
}

func (e *env) unrevokedSessions(t *testing.T, ft fixtureTenant, userID string) int {
	t.Helper()
	ids, err := e.sessions.NonRevokedIDsForUserInTenant(t.Context(), userID, ft.id)
	if err != nil {
		t.Fatalf("NonRevokedIDsForUserInTenant() error: %v", err)
	}
	return len(ids)
}

func (e *env) auditCount(t *testing.T, ft fixtureTenant, eventType, userID string) int {
	t.Helper()
	var n int
	if err := e.conn.QueryRow(`SELECT COUNT(*) FROM system.auth_audit_log WHERE tenant_id = $1 AND event_type = $2 AND user_id = $3`, ft.id, eventType, userID).Scan(&n); err != nil {
		t.Fatalf("count audit rows: %v", err)
	}
	return n
}

func (e *env) userStatus(t *testing.T, userID string) (status string, deleted bool) {
	t.Helper()
	var deletedAt *time.Time
	if err := e.conn.QueryRow(`SELECT status, deleted_at FROM system.users WHERE id = $1`, userID).Scan(&status, &deletedAt); err != nil {
		t.Fatalf("read user status: %v", err)
	}
	return status, deletedAt != nil
}

type request struct {
	serve  func(http.ResponseWriter, *http.Request)
	method string
	path   string
	params map[string]string
	body   any
}

func do(t *testing.T, ft fixtureTenant, token string, req request) *httptest.ResponseRecorder {
	t.Helper()
	var body bytes.Buffer
	if req.body != nil {
		if err := json.MarshalWrite(&body, req.body); err != nil {
			t.Fatalf("marshal body: %v", err)
		}
	}
	r := httptest.NewRequest(req.method, req.path, &body)
	r.Host = ft.domain
	r.RemoteAddr = "203.0.113.7:54321"
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	r = r.WithContext(route.WithParams(r.Context(), req.params))
	rec := httptest.NewRecorder()
	req.serve(rec, r)
	return rec
}

type listResponse struct {
	Data []userJSON `json:"data"`
	Meta struct {
		Total  int     `json:"total"`
		Cursor *string `json:"cursor"`
	} `json:"meta"`
}

func (e *env) list(t *testing.T, ft fixtureTenant, token, query string) listResponse {
	t.Helper()
	rec := do(t, ft, token, request{serve: e.handler.ServeList, method: http.MethodGet, path: "/admin/users" + query})
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /admin/users%s status = %d, body = %s", query, rec.Code, rec.Body)
	}
	var out listResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	return out
}

func ids(users []userJSON) []string {
	out := make([]string, len(users))
	for i, u := range users {
		out[i] = u.ID
	}
	return out
}

func userRoutes(h *Handler, id string) []request {
	idParams := map[string]string{"id": id}
	return []request{
		{serve: h.ServeList, method: http.MethodGet, path: "/admin/users"},
		{serve: h.ServeGet, method: http.MethodGet, path: "/admin/users/" + id, params: idParams},
		{serve: h.ServeSuspend, method: http.MethodPost, path: "/admin/users/" + id + "/suspend", params: idParams, body: map[string]string{"reason": "x"}},
		{serve: h.ServeUnsuspend, method: http.MethodPost, path: "/admin/users/" + id + "/unsuspend", params: idParams},
		{serve: h.ServeDelete, method: http.MethodDelete, path: "/admin/users/" + id, params: idParams},
		{serve: h.ServeSessions, method: http.MethodGet, path: "/admin/users/" + id + "/sessions", params: idParams},
		{
			serve: h.ServeRevokeSession, method: http.MethodDelete, path: "/admin/users/" + id + "/sessions/" + id,
			params: map[string]string{"id": id, "family_id": id},
		},
	}
}

func TestEveryRoute_RejectsNonAdminAndAnonymous(t *testing.T) {
	e := newEnv(t)
	ft := e.newTenant(t)
	caller := e.member(t, ft, "nonadmin", "Non Admin", "user")
	target := e.member(t, ft, "target", "Target", "user")
	token := e.issue(t, ft, caller)

	for _, req := range userRoutes(e.handler, target) {
		if rec := do(t, ft, token, req); rec.Code != http.StatusForbidden {
			t.Errorf("%s %s as non-admin: status = %d, want 403", req.method, req.path, rec.Code)
		}
		if rec := do(t, ft, "", req); rec.Code != http.StatusUnauthorized {
			t.Errorf("%s %s anonymous: status = %d, want 401", req.method, req.path, rec.Code)
		}
	}
	if status, _ := e.userStatus(t, target); status != "active" {
		t.Errorf("target status = %q after rejected requests, want active", status)
	}
}

func TestEveryTargetRoute_404sForAnotherTenantsUser(t *testing.T) {
	e := newEnv(t)
	ft := e.newTenant(t)
	other := e.newTenant(t)
	admin := e.member(t, ft, "admin", "Admin", "admin")
	outsider := e.member(t, other, "outsider", "Outsider", "user")
	e.issue(t, other, outsider)
	token := e.issue(t, ft, admin)

	for _, req := range userRoutes(e.handler, outsider)[1:] {
		if rec := do(t, ft, token, req); rec.Code != http.StatusNotFound {
			t.Errorf("%s %s: status = %d, want 404", req.method, req.path, rec.Code)
		}
	}
	if slices.Contains(ids(e.list(t, ft, token, "").Data), outsider) {
		t.Error("GET /admin/users lists another tenant's user")
	}
	if status, _ := e.userStatus(t, outsider); status != "active" {
		t.Errorf("outsider status = %q, want active", status)
	}
	if n := len(e.familyIDs(t, outsider)); n != 1 {
		t.Fatalf("outsider has %d sessions, want 1", n)
	}
	if !e.authenticates(t, other, e.issue(t, other, outsider)) {
		t.Error("outsider's session in their own tenant stopped working")
	}
}

func TestServeList_MembersInviteesFiltersAndPagination(t *testing.T) {
	e := newEnv(t)
	ft := e.newTenant(t)
	ctx := t.Context()
	stamp := time.Now().UnixNano()

	admin := e.createUser(t, fmt.Sprintf("a-admin%d@example.com", stamp), "Ada Admin")
	e.grant(t, ft, admin, "admin")
	active := e.createUser(t, fmt.Sprintf("b-active%d@example.com", stamp), "Bola Active")
	e.grant(t, ft, active, "user")
	e.grant(t, ft, active, "portal")
	suspended := e.createUser(t, fmt.Sprintf("c-suspended%d@example.com", stamp), "Chidi Suspended")
	e.grant(t, ft, suspended, "user")
	if _, err := e.conn.Exec(`UPDATE system.users SET status = 'suspended' WHERE id = $1`, suspended); err != nil {
		t.Fatalf("suspend fixture: %v", err)
	}
	deleted := e.createUser(t, fmt.Sprintf("d-deleted%d@example.com", stamp), "Dele Deleted")
	e.grant(t, ft, deleted, "user")
	if _, err := e.conn.Exec(`UPDATE system.users SET status = 'deleted', deleted_at = NOW() WHERE id = $1`, deleted); err != nil {
		t.Fatalf("delete fixture: %v", err)
	}
	inviteeEmail := fmt.Sprintf("e-invitee%d@example.com", stamp)
	inv, err := e.invites.Invite(ctx, ft.slug, inviteeEmail, "user", "Efua Invitee", nil)
	if err != nil {
		t.Fatalf("Invite() error: %v", err)
	}
	invitee, err := e.users.GetByEmail(ctx, inviteeEmail)
	if err != nil {
		t.Fatalf("GetByEmail(invitee) error: %v", err)
	}
	t.Cleanup(func() { _, _ = e.conn.Exec(`DELETE FROM system.users WHERE id = $1`, invitee.ID) })
	token := e.issue(t, ft, admin)

	all := e.list(t, ft, token, "")
	if want := []string{admin, active, suspended, invitee.ID}; !slices.Equal(ids(all.Data), want) || all.Meta.Total != 4 {
		t.Fatalf("list = %v (total %d), want %v (total 4)", ids(all.Data), all.Meta.Total, want)
	}
	if all.Meta.Cursor != nil {
		t.Errorf("single-page cursor = %q, want null", *all.Meta.Cursor)
	}
	byID := map[string]userJSON{}
	for _, u := range all.Data {
		byID[u.ID] = u
	}
	if got := byID[active]; got.Status != "active" || !slices.Equal(got.Roles, []string{"portal", "user"}) || got.Name == nil || *got.Name != "Bola Active" {
		t.Errorf("active row = %+v", got)
	}
	if got := byID[suspended]; got.Status != "suspended" {
		t.Errorf("suspended row status = %q", got.Status)
	}
	if got := byID[invitee.ID]; got.Status != "invited" || got.InvitationID == nil || *got.InvitationID != inv.ID || len(got.Roles) != 0 {
		t.Errorf("invitee row = %+v, want invited with invitation_id %s", got, inv.ID)
	}

	for query, want := range map[string][]string{
		"?status=invited":   {invitee.ID},
		"?status=suspended": {suspended},
		"?status=active":    {admin, active},
		"?q=bola":           {active},
		"?q=C-SUSPENDED":    {suspended},
		"?q=%25":            {},
	} {
		got := e.list(t, ft, token, query)
		if !slices.Equal(ids(got.Data), want) || got.Meta.Total != len(want) {
			t.Errorf("list%s = %v (total %d), want %v", query, ids(got.Data), got.Meta.Total, want)
		}
	}

	var paged []string
	query := "?limit=3"
	for range 3 {
		page := e.list(t, ft, token, query)
		if page.Meta.Total != 4 {
			t.Errorf("paged total = %d, want 4", page.Meta.Total)
		}
		paged = append(paged, ids(page.Data)...)
		if page.Meta.Cursor == nil {
			break
		}
		query = "?limit=3&cursor=" + *page.Meta.Cursor
	}
	if !slices.Equal(paged, ids(all.Data)) {
		t.Errorf("paged ids = %v, want %v", paged, ids(all.Data))
	}

	for _, bad := range []string{"?status=deleted", "?limit=0", "?limit=101", "?cursor=***"} {
		rec := do(t, ft, token, request{serve: e.handler.ServeList, method: http.MethodGet, path: "/admin/users" + bad})
		if rec.Code != http.StatusBadRequest {
			t.Errorf("list%s status = %d, want 400", bad, rec.Code)
		}
	}
}

func TestServeGet_MemberAndInviteeDetail(t *testing.T) {
	e := newEnv(t)
	ft := e.newTenant(t)
	ctx := t.Context()
	admin := e.member(t, ft, "admin", "Admin", "admin")
	target := e.member(t, ft, "target", "Target Person", "user")
	if _, err := e.conn.Exec(`UPDATE system.user_profiles SET phone = '+233200000000' WHERE user_id = $1`, target); err != nil {
		t.Fatalf("set phone: %v", err)
	}
	inviteeEmail := fmt.Sprintf("invitee%d@example.com", time.Now().UnixNano())
	inv, err := e.invites.Invite(ctx, ft.slug, inviteeEmail, "portal", "", nil)
	if err != nil {
		t.Fatalf("Invite() error: %v", err)
	}
	invitee, err := e.users.GetByEmail(ctx, inviteeEmail)
	if err != nil {
		t.Fatalf("GetByEmail() error: %v", err)
	}
	t.Cleanup(func() { _, _ = e.conn.Exec(`DELETE FROM system.users WHERE id = $1`, invitee.ID) })
	token := e.issue(t, ft, admin)

	get := func(id string) (*httptest.ResponseRecorder, userDetailJSON) {
		rec := do(t, ft, token, request{serve: e.handler.ServeGet, method: http.MethodGet, path: "/admin/users/" + id, params: map[string]string{"id": id}})
		var out userDetailJSON
		if rec.Code == http.StatusOK {
			if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
				t.Fatalf("decode detail: %v", err)
			}
		}
		return rec, out
	}

	rec, got := get(target)
	if rec.Code != http.StatusOK || got.Phone == nil || *got.Phone != "+233200000000" || got.Status != "active" || got.Invitation != nil || !slices.Equal(got.Roles, []string{"user"}) {
		t.Errorf("member detail = %d %+v", rec.Code, got)
	}
	rec, got = get(invitee.ID)
	if rec.Code != http.StatusOK || got.Status != "invited" || got.Invitation == nil || got.Invitation.ID != inv.ID || got.Invitation.Role != "portal" {
		t.Errorf("invitee detail = %d %+v", rec.Code, got)
	}
	for _, id := range []string{"not-a-uuid", uuid.New().String()} {
		if rec, _ := get(id); rec.Code != http.StatusNotFound {
			t.Errorf("GET /admin/users/%s status = %d, want 404", id, rec.Code)
		}
	}
}

func TestServeSuspendAndUnsuspend(t *testing.T) {
	e := newEnv(t)
	ft := e.newTenant(t)
	admin := e.member(t, ft, "admin", "Admin", "admin")
	target := e.member(t, ft, "target", "Target", "user")
	adminToken := e.issue(t, ft, admin)
	targetToken := e.issue(t, ft, target)
	params := map[string]string{"id": target}
	suspend := func(body any) *httptest.ResponseRecorder {
		return do(t, ft, adminToken, request{serve: e.handler.ServeSuspend, method: http.MethodPost, path: "/admin/users/" + target + "/suspend", params: params, body: body})
	}
	unsuspend := func() *httptest.ResponseRecorder {
		return do(t, ft, adminToken, request{serve: e.handler.ServeUnsuspend, method: http.MethodPost, path: "/admin/users/" + target + "/unsuspend", params: params})
	}

	if rec := suspend(map[string]string{"reason": "  "}); rec.Code != http.StatusBadRequest {
		t.Errorf("suspend without reason: status = %d, want 400", rec.Code)
	}
	if rec := unsuspend(); rec.Code != http.StatusConflict {
		t.Errorf("unsuspend of an active user: status = %d, want 409", rec.Code)
	}
	if status, _ := e.userStatus(t, target); status != "active" {
		t.Fatalf("status = %q, want active", status)
	}

	if rec := suspend(map[string]string{"reason": "left the company"}); rec.Code != http.StatusNoContent {
		t.Fatalf("suspend: status = %d, body = %s", rec.Code, rec.Body)
	}
	if status, _ := e.userStatus(t, target); status != "suspended" {
		t.Errorf("status = %q, want suspended", status)
	}
	if e.authenticates(t, ft, targetToken) {
		t.Error("suspended user's access token still authenticates")
	}
	if n := e.unrevokedSessions(t, ft, target); n != 0 {
		t.Errorf("suspended user has %d unrevoked sessions, want 0", n)
	}
	var reason string
	if err := e.conn.QueryRow(`SELECT metadata->>'reason' FROM system.auth_audit_log WHERE tenant_id = $1 AND event_type = 'user.suspended' AND user_id = $2`, ft.id, target).Scan(&reason); err != nil || reason != "left the company" {
		t.Errorf("user.suspended audit reason = %q, err = %v", reason, err)
	}
	if rec := suspend(map[string]string{"reason": "again"}); rec.Code != http.StatusConflict {
		t.Errorf("second suspend: status = %d, want 409", rec.Code)
	}
	if n := e.auditCount(t, ft, "user.suspended", target); n != 1 {
		t.Errorf("user.suspended rows = %d, want 1", n)
	}

	if rec := unsuspend(); rec.Code != http.StatusNoContent {
		t.Fatalf("unsuspend: status = %d, body = %s", rec.Code, rec.Body)
	}
	if status, _ := e.userStatus(t, target); status != "active" {
		t.Errorf("status = %q, want active", status)
	}
	if n := e.auditCount(t, ft, "user.unsuspended", target); n != 1 {
		t.Errorf("user.unsuspended rows = %d, want 1", n)
	}
	if !e.authenticates(t, ft, e.issue(t, ft, target)) {
		t.Error("unsuspended user's new session doesn't authenticate")
	}
}

func TestServeDelete(t *testing.T) {
	e := newEnv(t)
	ft := e.newTenant(t)
	admin := e.member(t, ft, "admin", "Admin", "admin")
	target := e.member(t, ft, "target", "Target", "user")
	adminToken := e.issue(t, ft, admin)
	targetToken := e.issue(t, ft, target)

	rec := do(t, ft, adminToken, request{serve: e.handler.ServeDelete, method: http.MethodDelete, path: "/admin/users/" + target, params: map[string]string{"id": target}})
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete: status = %d, body = %s", rec.Code, rec.Body)
	}
	if status, deleted := e.userStatus(t, target); status != "deleted" || !deleted {
		t.Errorf("status = %q, deleted_at set = %v, want deleted and set", status, deleted)
	}
	if e.authenticates(t, ft, targetToken) {
		t.Error("deleted user's access token still authenticates")
	}
	if n := e.unrevokedSessions(t, ft, target); n != 0 {
		t.Errorf("deleted user has %d unrevoked sessions, want 0", n)
	}
	if n := e.auditCount(t, ft, "user.deleted", target); n != 1 {
		t.Errorf("user.deleted rows = %d, want 1", n)
	}
	if slices.Contains(ids(e.list(t, ft, adminToken, "").Data), target) {
		t.Error("deleted user is still listed")
	}
	rec = do(t, ft, adminToken, request{serve: e.handler.ServeDelete, method: http.MethodDelete, path: "/admin/users/" + target, params: map[string]string{"id": target}})
	if rec.Code != http.StatusNotFound {
		t.Errorf("second delete: status = %d, want 404", rec.Code)
	}
}

func TestSuspendAndDelete_RejectSelf(t *testing.T) {
	e := newEnv(t)
	ft := e.newTenant(t)
	admin := e.member(t, ft, "admin", "Admin", "admin")
	token := e.issue(t, ft, admin)
	params := map[string]string{"id": admin}

	for _, req := range []request{
		{serve: e.handler.ServeSuspend, method: http.MethodPost, path: "/admin/users/" + admin + "/suspend", params: params, body: map[string]string{"reason": "x"}},
		{serve: e.handler.ServeDelete, method: http.MethodDelete, path: "/admin/users/" + admin, params: params},
	} {
		if rec := do(t, ft, token, req); rec.Code != http.StatusBadRequest {
			t.Errorf("%s %s on self: status = %d, want 400", req.method, req.path, rec.Code)
		}
	}
	if status, _ := e.userStatus(t, admin); status != "active" {
		t.Errorf("admin status = %q, want active", status)
	}
}

func TestServeSessionsAndRevokeSession(t *testing.T) {
	e := newEnv(t)
	ft := e.newTenant(t)
	admin := e.member(t, ft, "admin", "Admin", "admin")
	target := e.member(t, ft, "target", "Target", "user")
	bystander := e.member(t, ft, "bystander", "Bystander", "user")
	adminToken := e.issue(t, ft, admin)
	firstToken := e.issue(t, ft, target)
	secondToken := e.issue(t, ft, target)
	e.issue(t, ft, bystander)
	families := e.familyIDs(t, target)

	rec := do(t, ft, adminToken, request{serve: e.handler.ServeSessions, method: http.MethodGet, path: "/admin/users/" + target + "/sessions", params: map[string]string{"id": target}})
	var listed struct {
		Sessions []sessionJSON `json:"sessions"`
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("sessions: status = %d, body = %s", rec.Code, rec.Body)
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &listed); err != nil {
		t.Fatalf("decode sessions: %v", err)
	}
	got := make([]string, len(listed.Sessions))
	for i, s := range listed.Sessions {
		got[i] = s.ID
		if s.Current {
			t.Errorf("session %s marked current in another user's list", s.ID)
		}
	}
	slices.Sort(got)
	want := slices.Sorted(slices.Values(families))
	if !slices.Equal(got, want) {
		t.Fatalf("listed families = %v, want %v", got, want)
	}

	revoke := func(token, familyID string) *httptest.ResponseRecorder {
		return do(t, ft, token, request{
			serve: e.handler.ServeRevokeSession, method: http.MethodDelete, path: "/admin/users/" + target + "/sessions/" + familyID,
			params: map[string]string{"id": target, "family_id": familyID},
		})
	}
	for _, familyID := range []string{uuid.New().String(), e.familyIDs(t, bystander)[0], "not-a-uuid"} {
		if rec := revoke(adminToken, familyID); rec.Code != http.StatusNotFound {
			t.Errorf("revoke %s: status = %d, want 404", familyID, rec.Code)
		}
	}
	if rec := revoke(adminToken, families[0]); rec.Code != http.StatusNoContent {
		t.Fatalf("revoke: status = %d, body = %s", rec.Code, rec.Body)
	}
	if e.authenticates(t, ft, firstToken) {
		t.Error("revoked session's access token still authenticates")
	}
	if !e.authenticates(t, ft, secondToken) {
		t.Error("the target's other session stopped authenticating")
	}
	if n := e.auditCount(t, ft, "session.revoked", target); n != 1 {
		t.Errorf("session.revoked rows = %d, want 1", n)
	}
	if rec := revoke(adminToken, families[0]); rec.Code != http.StatusNotFound {
		t.Errorf("revoking an already-revoked family: status = %d, want 404", rec.Code)
	}
}

func TestServeSessions_AdminsOwnListMarksAndProtectsCurrent(t *testing.T) {
	e := newEnv(t)
	ft := e.newTenant(t)
	admin := e.member(t, ft, "admin", "Admin", "admin")
	e.issue(t, ft, admin)
	token := e.issue(t, ft, admin)
	families := e.familyIDs(t, admin)
	current := families[len(families)-1]

	rec := do(t, ft, token, request{serve: e.handler.ServeSessions, method: http.MethodGet, path: "/admin/users/" + admin + "/sessions", params: map[string]string{"id": admin}})
	var listed struct {
		Sessions []sessionJSON `json:"sessions"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &listed); err != nil {
		t.Fatalf("decode sessions: %v", err)
	}
	if len(listed.Sessions) != 2 || listed.Sessions[0].ID != current || !listed.Sessions[0].Current || listed.Sessions[1].Current {
		t.Fatalf("sessions = %+v, want current family %s first and only it current", listed.Sessions, current)
	}

	rec = do(t, ft, token, request{
		serve: e.handler.ServeRevokeSession, method: http.MethodDelete, path: "/admin/users/" + admin + "/sessions/" + current,
		params: map[string]string{"id": admin, "family_id": current},
	})
	if rec.Code != http.StatusBadRequest {
		t.Errorf("revoking own current session: status = %d, want 400", rec.Code)
	}
	if !e.authenticates(t, ft, token) {
		t.Error("current session stopped authenticating")
	}
}

// rotate simulates a refresh: familyID's live row is marked rotated and a
// successor row joins the same family.
func (e *env) rotate(t *testing.T, ft fixtureTenant, userID, familyID string) string {
	t.Helper()
	successor := uuid.New().String()
	if _, err := e.conn.Exec(`UPDATE system.sessions SET rotated_at = NOW() WHERE family_id = $1 AND rotated_at IS NULL AND revoked_at IS NULL`, familyID); err != nil {
		t.Fatalf("rotate: %v", err)
	}
	if _, err := e.conn.Exec(`
		INSERT INTO system.sessions (id, user_id, tenant_id, family_id, device_id, refresh_hash, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6, NOW() + INTERVAL '1 day')
	`, successor, userID, ft.id, familyID, uuid.New().String(), uuid.New().String()); err != nil {
		t.Fatalf("insert successor: %v", err)
	}
	return successor
}

func TestServeRevokeSession_RevokesTheWholeFamilyAfterRotation(t *testing.T) {
	e := newEnv(t)
	ft := e.newTenant(t)
	admin := e.member(t, ft, "admin", "Admin", "admin")
	target := e.member(t, ft, "target", "Target", "user")
	adminToken := e.issue(t, ft, admin)
	e.issue(t, ft, target)
	family := e.familyIDs(t, target)[0]
	successor := e.rotate(t, ft, target, family)

	rec := do(t, ft, adminToken, request{
		serve: e.handler.ServeRevokeSession, method: http.MethodDelete, path: "/admin/users/" + target + "/sessions/" + family,
		params: map[string]string{"id": target, "family_id": family},
	})
	if rec.Code != http.StatusNoContent {
		t.Fatalf("revoke: status = %d, body = %s", rec.Code, rec.Body)
	}
	if n := e.unrevokedSessions(t, ft, target); n != 0 {
		t.Errorf("target has %d unrevoked rows after revoking the family, want 0", n)
	}
	if blocked, err := e.handler.revoker.IsBlocked(t.Context(), successor); err != nil || !blocked {
		t.Errorf("successor row blocklisted = %v, err = %v, want true", blocked, err)
	}
}

func TestServeSessions_CurrentFollowsTheCallersFamilyAfterRotation(t *testing.T) {
	e := newEnv(t)
	ft := e.newTenant(t)
	admin := e.member(t, ft, "admin", "Admin", "admin")
	token := e.issue(t, ft, admin)
	family := e.familyIDs(t, admin)[0]
	e.rotate(t, ft, admin, family)

	rec := do(t, ft, token, request{serve: e.handler.ServeSessions, method: http.MethodGet, path: "/admin/users/" + admin + "/sessions", params: map[string]string{"id": admin}})
	var listed struct {
		Sessions []sessionJSON `json:"sessions"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &listed); err != nil {
		t.Fatalf("decode sessions: %v", err)
	}
	if len(listed.Sessions) != 1 || listed.Sessions[0].ID != family || !listed.Sessions[0].Current {
		t.Fatalf("sessions = %+v, want family %s marked current", listed.Sessions, family)
	}
	rec = do(t, ft, token, request{
		serve: e.handler.ServeRevokeSession, method: http.MethodDelete, path: "/admin/users/" + admin + "/sessions/" + family,
		params: map[string]string{"id": admin, "family_id": family},
	})
	if rec.Code != http.StatusBadRequest {
		t.Errorf("revoking own rotated current session: status = %d, want 400", rec.Code)
	}
}

func TestServeSuspend_OnlyAppliesToActiveUsers(t *testing.T) {
	e := newEnv(t)
	ft := e.newTenant(t)
	admin := e.member(t, ft, "admin", "Admin", "admin")
	target := e.member(t, ft, "target", "Target", "user")
	if _, err := e.conn.Exec(`UPDATE system.users SET status = 'pending_verification' WHERE id = $1`, target); err != nil {
		t.Fatalf("set pending_verification: %v", err)
	}
	token := e.issue(t, ft, admin)

	rec := do(t, ft, token, request{
		serve: e.handler.ServeSuspend, method: http.MethodPost, path: "/admin/users/" + target + "/suspend",
		params: map[string]string{"id": target}, body: map[string]string{"reason": "x"},
	})
	if rec.Code != http.StatusConflict {
		t.Errorf("suspend of a pending_verification user: status = %d, want 409", rec.Code)
	}
	if status, _ := e.userStatus(t, target); status != "pending_verification" {
		t.Errorf("status = %q, want pending_verification", status)
	}
}
