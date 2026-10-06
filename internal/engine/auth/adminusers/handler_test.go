package adminusers

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json/v2"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
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
	"github.com/djangbahevans/goerp/internal/engine/authaudit/audittest"
	"github.com/djangbahevans/goerp/internal/engine/billing"
	"github.com/djangbahevans/goerp/internal/engine/cache"
	"github.com/djangbahevans/goerp/internal/engine/db"
	"github.com/djangbahevans/goerp/internal/engine/invite"
	"github.com/djangbahevans/goerp/internal/engine/permcache"
	"github.com/djangbahevans/goerp/internal/engine/permission"
	"github.com/djangbahevans/goerp/internal/engine/role"
	"github.com/djangbahevans/goerp/internal/engine/route"
	"github.com/djangbahevans/goerp/internal/engine/tenant"
	tenantresolve "github.com/djangbahevans/goerp/internal/engine/tenant/resolve"
	"github.com/djangbahevans/goerp/internal/engine/tenantschema"
	"github.com/djangbahevans/goerp/internal/engine/user"
)

const localPostgresDSN = "postgres://goerp:dev@localhost:15432/goerp"

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
		handler:  NewHandler(resolver, checker, NewStore(conn, auditStore), roleStore, permcache.NewRoleCache(cacheClient), sessionStore, revoker, inviteStore, userStore, nil, nil, modelForTable),
		roleMap:  roleMap,
		mailer:   mailer,
	}
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
	if err := e.roles.AddMember(t.Context(), ft.slug, userID); err != nil {
		t.Fatalf("AddMember() error: %v", err)
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
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate families: %v", err)
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

// memberStatus returns userID's tenant_members status in ft, or "" when
// they have no member row there.
func (e *env) memberStatus(t *testing.T, ft fixtureTenant, userID string) string {
	t.Helper()
	var status string
	err := e.conn.QueryRow(fmt.Sprintf(`SELECT status FROM %s.tenant_members WHERE user_id = $1`, tenantschema.Name(ft.slug)), userID).Scan(&status)
	if errors.Is(err, sql.ErrNoRows) {
		return ""
	}
	if err != nil {
		t.Fatalf("read member status: %v", err)
	}
	return status
}

func (e *env) setMemberStatus(t *testing.T, ft fixtureTenant, userID, status string) {
	t.Helper()
	if _, err := e.conn.Exec(fmt.Sprintf(`UPDATE %s.tenant_members SET status = $2 WHERE user_id = $1`, tenantschema.Name(ft.slug)), userID, status); err != nil {
		t.Fatalf("set member status: %v", err)
	}
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
		{serve: h.ServeActivity, method: http.MethodGet, path: "/admin/users/" + id + "/activity", params: idParams},
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
	accountSuspended := e.createUser(t, fmt.Sprintf("bb-account%d@example.com", stamp), "Bisi Account")
	e.grant(t, ft, accountSuspended, "user")
	if _, err := e.conn.Exec(`UPDATE system.users SET status = 'suspended' WHERE id = $1`, accountSuspended); err != nil {
		t.Fatalf("suspend fixture account: %v", err)
	}
	suspended := e.createUser(t, fmt.Sprintf("c-suspended%d@example.com", stamp), "Chidi Suspended")
	e.grant(t, ft, suspended, "user")
	e.setMemberStatus(t, ft, suspended, "suspended")
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
	if want := []string{admin, active, accountSuspended, suspended, invitee.ID}; !slices.Equal(ids(all.Data), want) || all.Meta.Total != 5 {
		t.Fatalf("list = %v (total %d), want %v (total 5)", ids(all.Data), all.Meta.Total, want)
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
	if got := byID[suspended]; got.Status != "suspended" || got.AccountSuspended {
		t.Errorf("suspended row = %+v, want member status suspended, account not suspended", got)
	}
	if got := byID[accountSuspended]; got.Status != "active" || !got.AccountSuspended {
		t.Errorf("account-suspended row = %+v, want member status active with account_suspended", got)
	}
	if got := byID[invitee.ID]; got.Status != "invited" || got.InvitationID == nil || *got.InvitationID != inv.ID || len(got.Roles) != 0 {
		t.Errorf("invitee row = %+v, want invited with invitation_id %s", got, inv.ID)
	}

	for query, want := range map[string][]string{
		"?status=invited":          {invitee.ID},
		"?status=suspended":        {suspended},
		"?status=active":           {admin, active, accountSuspended},
		"?q=bola":                  {active},
		"?q=C-SUSPENDED":           {suspended},
		"?q=%25":                   {},
		"?role=portal":             {active},
		"?role=user":               {active, accountSuspended, suspended},
		"?role=user&status=active": {active, accountSuspended},
		"?role=nosuch":             {},
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
		if page.Meta.Total != 5 {
			t.Errorf("paged total = %d, want 5", page.Meta.Total)
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
	if _, err := e.conn.Exec(fmt.Sprintf(`UPDATE %s.tenant_members SET phone = '+233200000000', job_title = 'Bookkeeper' WHERE user_id = $1`, tenantschema.Name(ft.slug)), target); err != nil {
		t.Fatalf("set phone and job title: %v", err)
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
	if rec.Code != http.StatusOK || got.Phone == nil || *got.Phone != "+233200000000" || got.JobTitle == nil || *got.JobTitle != "Bookkeeper" || got.Status != "active" || got.Invitation != nil || !slices.Equal(got.Roles, []string{"user"}) {
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
	if status := e.memberStatus(t, ft, target); status != "active" {
		t.Fatalf("member status = %q, want active", status)
	}

	if rec := suspend(map[string]string{"reason": "left the company"}); rec.Code != http.StatusNoContent {
		t.Fatalf("suspend: status = %d, body = %s", rec.Code, rec.Body)
	}
	if status := e.memberStatus(t, ft, target); status != "suspended" {
		t.Errorf("member status = %q, want suspended", status)
	}
	if status, _ := e.userStatus(t, target); status != "active" {
		t.Errorf("account status = %q, want active: a tenant admin never changes it", status)
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
	audittest.AssertLatest(t, e.conn, ft.id, "user.suspended", target, admin)

	if rec := unsuspend(); rec.Code != http.StatusNoContent {
		t.Fatalf("unsuspend: status = %d, body = %s", rec.Code, rec.Body)
	}
	if status := e.memberStatus(t, ft, target); status != "active" {
		t.Errorf("member status = %q, want active", status)
	}
	if n := e.auditCount(t, ft, "user.unsuspended", target); n != 1 {
		t.Errorf("user.unsuspended rows = %d, want 1", n)
	}
	audittest.AssertLatest(t, e.conn, ft.id, "user.unsuspended", target, admin)
	unsuspendedToken := e.issue(t, ft, target)
	if !e.authenticates(t, ft, unsuspendedToken) {
		t.Error("unsuspended user's new session doesn't authenticate")
	}
	if roles, err := e.roles.RoleNamesForUser(t.Context(), ft.slug, target); err != nil || !slices.Equal(roles, []string{"user"}) {
		t.Errorf("roles after unsuspend = %v (err %v), want the same [user]", roles, err)
	}
}

func TestServeDelete_RemovesTheMembership(t *testing.T) {
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
	if status := e.memberStatus(t, ft, target); status != "" {
		t.Errorf("member status = %q after removal, want no member row", status)
	}
	if roles, err := e.roles.RoleNamesForUser(t.Context(), ft.slug, target); err != nil || len(roles) != 0 {
		t.Errorf("roles after removal = %v (err %v), want none", roles, err)
	}
	if status, deleted := e.userStatus(t, target); status != "active" || deleted {
		t.Errorf("account status = %q, deleted = %v, want active and not deleted", status, deleted)
	}
	if e.authenticates(t, ft, targetToken) {
		t.Error("removed member's access token still authenticates")
	}
	if n := e.unrevokedSessions(t, ft, target); n != 0 {
		t.Errorf("removed member has %d unrevoked sessions, want 0", n)
	}
	if n := e.auditCount(t, ft, "user.removed", target); n != 1 {
		t.Errorf("user.removed rows = %d, want 1", n)
	}
	audittest.AssertLatest(t, e.conn, ft.id, "user.removed", target, admin)
	if slices.Contains(ids(e.list(t, ft, adminToken, "").Data), target) {
		t.Error("removed member is still listed")
	}
	rec = do(t, ft, adminToken, request{serve: e.handler.ServeDelete, method: http.MethodDelete, path: "/admin/users/" + target, params: map[string]string{"id": target}})
	if rec.Code != http.StatusNotFound {
		t.Errorf("second delete: status = %d, want 404", rec.Code)
	}
}

// TestMemberActions_LeaveTheAccountsOtherTenantAlone covers a two-tenant
// account: suspending or removing it in one tenant changes nothing about
// the account or its access to the other.
func TestMemberActions_LeaveTheAccountsOtherTenantAlone(t *testing.T) {
	e := newEnv(t)
	a := e.newTenant(t)
	b := e.newTenant(t)
	adminA := e.member(t, a, "admina", "Admin A", "admin")
	shared := e.member(t, a, "shared", "Shared Bookkeeper", "user")
	e.grant(t, b, shared, "user")
	tokenA := e.issue(t, a, adminA)
	tokenB := e.issue(t, b, shared)
	params := map[string]string{"id": shared}

	rec := do(t, a, tokenA, request{serve: e.handler.ServeSuspend, method: http.MethodPost, path: "/admin/users/" + shared + "/suspend", params: params, body: map[string]string{"reason": "contract ended"}})
	if rec.Code != http.StatusNoContent {
		t.Fatalf("suspend in A: status = %d, body = %s", rec.Code, rec.Body)
	}
	if isMember, err := e.roles.IsMember(t.Context(), a.slug, shared); err != nil || isMember {
		t.Errorf("IsMember(A) after suspension = %v (err %v), want false", isMember, err)
	}
	e.assertUntouchedIn(t, b, shared, tokenB)

	rec = do(t, a, tokenA, request{serve: e.handler.ServeDelete, method: http.MethodDelete, path: "/admin/users/" + shared, params: params})
	if rec.Code != http.StatusNoContent {
		t.Fatalf("remove from A: status = %d, body = %s", rec.Code, rec.Body)
	}
	e.assertUntouchedIn(t, b, shared, tokenB)
}

func (e *env) assertUntouchedIn(t *testing.T, ft fixtureTenant, userID, token string) {
	t.Helper()
	if status, deleted := e.userStatus(t, userID); status != "active" || deleted {
		t.Errorf("account status = %q, deleted = %v, want active", status, deleted)
	}
	if status := e.memberStatus(t, ft, userID); status != "active" {
		t.Errorf("member status in the other tenant = %q, want active", status)
	}
	if n := e.unrevokedSessions(t, ft, userID); n != 1 {
		t.Errorf("sessions in the other tenant = %d, want 1", n)
	}
	if !e.authenticates(t, ft, token) {
		t.Error("session in the other tenant stopped authenticating")
	}
}

// TestStore_RefusesToSuspendOrRemoveTheLastActiveAdmin reaches the guard
// directly: through the handler the caller is always an active admin
// other than the target, so only a concurrent change can leave the target
// the last one.
func TestStore_RefusesToSuspendOrRemoveTheLastActiveAdmin(t *testing.T) {
	e := newEnv(t)
	ft := e.newTenant(t)
	ctx := t.Context()
	onlyAdmin := e.member(t, ft, "admin", "Admin", "admin")
	store := e.handler.store
	row := authaudit.Row{EventType: "user.suspended", TenantID: ft.id, UserID: onlyAdmin, Success: true}

	if err := store.suspend(ctx, ft.slug, onlyAdmin, "", "x", row); !errors.Is(err, role.ErrLastAdmin) {
		t.Errorf("suspend() error = %v, want ErrLastAdmin", err)
	}
	if err := store.remove(ctx, ft.slug, onlyAdmin, row); !errors.Is(err, role.ErrLastAdmin) {
		t.Errorf("remove() error = %v, want ErrLastAdmin", err)
	}
	if status := e.memberStatus(t, ft, onlyAdmin); status != "active" {
		t.Errorf("member status = %q, want active", status)
	}

	second := e.member(t, ft, "second", "Second", "admin")
	if err := store.suspend(ctx, ft.slug, onlyAdmin, second, "x", row); err != nil {
		t.Errorf("suspend() with another active admin error = %v, want nil", err)
	}
	if err := store.remove(ctx, ft.slug, second, row); !errors.Is(err, role.ErrLastAdmin) {
		t.Errorf("remove() of the admin left after a suspension error = %v, want ErrLastAdmin", err)
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
	audittest.AssertLatest(t, e.conn, ft.id, "session.revoked", target, admin)
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

func TestServeSuspend_LeavesAPendingAccountPending(t *testing.T) {
	e := newEnv(t)
	ft := e.newTenant(t)
	admin := e.member(t, ft, "admin", "Admin", "admin")
	target := e.member(t, ft, "target", "Target", "user")
	if _, err := e.conn.Exec(`UPDATE system.users SET status = 'pending_verification' WHERE id = $1`, target); err != nil {
		t.Fatalf("set pending_verification: %v", err)
	}
	token := e.issue(t, ft, admin)
	params := map[string]string{"id": target}

	rec := do(t, ft, token, request{
		serve: e.handler.ServeSuspend, method: http.MethodPost, path: "/admin/users/" + target + "/suspend",
		params: params, body: map[string]string{"reason": "x"},
	})
	if rec.Code != http.StatusNoContent {
		t.Fatalf("suspend: status = %d, body = %s", rec.Code, rec.Body)
	}
	rec = do(t, ft, token, request{serve: e.handler.ServeUnsuspend, method: http.MethodPost, path: "/admin/users/" + target + "/unsuspend", params: params})
	if rec.Code != http.StatusNoContent {
		t.Fatalf("unsuspend: status = %d, body = %s", rec.Code, rec.Body)
	}
	if status, _ := e.userStatus(t, target); status != "pending_verification" {
		t.Errorf("account status = %q, want pending_verification", status)
	}
}

func TestLastLoginAt_IsTheSignInToThisTenant(t *testing.T) {
	e := newEnv(t)
	a := e.newTenant(t)
	b := e.newTenant(t)
	adminA := e.member(t, a, "admina", "Admin A", "admin")
	shared := e.member(t, a, "shared", "Shared", "user")
	e.grant(t, b, shared, "user")
	token := e.issue(t, a, adminA)

	detail := func() userDetailJSON {
		rec := do(t, a, token, request{serve: e.handler.ServeGet, method: http.MethodGet, path: "/admin/users/" + shared, params: map[string]string{"id": shared}})
		var out userDetailJSON
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatalf("decode detail: %v (status %d)", err, rec.Code)
		}
		return out
	}

	e.issue(t, b, shared)
	if got := detail(); got.LastLoginAt != nil {
		t.Errorf("last_login_at in A after signing in to B = %v, want nil", got.LastLoginAt)
	}
	e.issue(t, a, shared)
	if got := detail(); got.LastLoginAt == nil {
		t.Error("last_login_at in A after signing in to A = nil, want set")
	}
}
