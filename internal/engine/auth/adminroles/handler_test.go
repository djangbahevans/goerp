package adminroles

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json/v2"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
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
	"github.com/djangbahevans/goerp/internal/engine/manifest"
	"github.com/djangbahevans/goerp/internal/engine/module"
	"github.com/djangbahevans/goerp/internal/engine/permcache"
	"github.com/djangbahevans/goerp/internal/engine/permission"
	"github.com/djangbahevans/goerp/internal/engine/registry"
	"github.com/djangbahevans/goerp/internal/engine/role"
	"github.com/djangbahevans/goerp/internal/engine/route"
	"github.com/djangbahevans/goerp/internal/engine/secrets"
	"github.com/djangbahevans/goerp/internal/engine/tenant"
	tenantresolve "github.com/djangbahevans/goerp/internal/engine/tenant/resolve"
	"github.com/djangbahevans/goerp/internal/engine/tenantschema"
	"github.com/djangbahevans/goerp/internal/engine/user"
)

const localPostgresDSN = "postgres://goerp:dev@localhost:55432/goerp"

const (
	permRead   = "rolestest:widget:read"
	permWrite  = "rolestest:widget:write"
	permHidden = "rolestesthidden:gadget:read"
)

type env struct {
	conn     *sql.DB
	tenants  *tenant.Store
	users    *user.Store
	roles    *role.Store
	billing  *billing.Store
	invites  *invite.Store
	issuer   *authtoken.Issuer
	checker  *authcheck.Checker
	modules  *registry.ModuleRegistry
	perms    *permcache.RolePermissionMap
	handler  *Handler
	registry func() *permission.PermissionRegistry
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

	modules := &registry.ModuleRegistry{}
	ready := func(name string, perms []manifest.Permission) *module.LoadedModule {
		return &module.LoadedModule{Status: module.StatusReady, Manifest: manifest.Manifest{Name: name, Type: "standard", Permissions: perms}}
	}
	if _, err := modules.Update(map[string]*module.LoadedModule{
		"rolestest": ready("rolestest", []manifest.Permission{
			{Name: permRead, Description: "View widgets", Category: "Widgets"},
			{Name: permWrite, Description: "Edit widgets", Category: "Widgets"},
		}),
		"rolestesthidden": ready("rolestesthidden", []manifest.Permission{{Name: permHidden, Description: "View gadgets"}}),
	}); err != nil {
		t.Fatalf("registry Update() error: %v", err)
	}
	currentRegistry := func() *permission.PermissionRegistry { return modules.Snapshot().PermissionRegistry() }

	roleStore := role.NewStore(conn)
	perms := permcache.NewRolePermissionMap()
	revoker := sessionrevoke.NewRevoker(sessionStore, cacheClient)
	checker := authcheck.NewChecker(&signingKeySet.Active, revoker, userStore, roleStore, permcache.NewRoleCache(cacheClient), perms, apiKeys, false, nil, nil, nil)
	resolver := tenantresolve.NewResolver(tenantStore, cacheClient, billingStore)

	return &env{
		conn:     conn,
		tenants:  tenantStore,
		users:    userStore,
		roles:    roleStore,
		billing:  billingStore,
		invites:  invite.NewStore(conn, userStore, roleStore, nil, nil),
		issuer:   authtoken.NewIssuer(&signingKeySet.Active, tenantStore, roleStore, sessionStore),
		checker:  checker,
		modules:  modules,
		perms:    perms,
		handler:  NewHandler(resolver, checker, roleStore, modules, perms, revoker, nil, auditStore),
		registry: currentRegistry,
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

	slug := fmt.Sprintf("adminrolestest%d", time.Now().UnixNano())
	tt, err := e.tenants.CreateTenant(ctx, slug, "Admin Roles Test Co")
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
	if err := e.billing.UpsertEntitlementOverride(ctx, tt.ID, "module.rolestest", "true", nil, nil, nil); err != nil {
		t.Fatalf("enable module: %v", err)
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
	if err := e.perms.RebuildTenant(ctx, e.roles, e.registry, slug); err != nil {
		t.Fatalf("RebuildTenant() error: %v", err)
	}
	return fixtureTenant{id: tt.ID, slug: slug, domain: domain}
}

func (e *env) member(t *testing.T, ft fixtureTenant, roleName string) string {
	t.Helper()
	ctx := t.Context()
	id, err := e.users.FindOrCreateInvited(ctx, fmt.Sprintf("adminroles%d@example.com", time.Now().UnixNano()))
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
	e.grant(t, ft, id, roleName)
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

func (e *env) issue(t *testing.T, ft fixtureTenant, userID string) string {
	t.Helper()
	tokens, err := e.issuer.Issue(t.Context(), authtoken.LoginParams{UserID: userID, TenantSlug: ft.slug, DeviceID: uuid.New().String()})
	if err != nil {
		t.Fatalf("Issue() error: %v", err)
	}
	return tokens.AccessToken
}

// can reports whether token's live permission set includes name.
func (e *env) can(t *testing.T, ft fixtureTenant, token, name string) bool {
	t.Helper()
	authCtx, err := e.checker.Authenticate(t.Context(), token, ft.id, ft.slug, "203.0.113.7", nil, nil)
	if err != nil || !authCtx.IsAuthenticated {
		t.Fatalf("Authenticate() = %+v, %v", authCtx, err)
	}
	idx, ok := e.registry().Index(name)
	return ok && authCtx.PermissionSet.Has(idx)
}

func (e *env) auditCount(t *testing.T, ft fixtureTenant, eventType string) int {
	t.Helper()
	var n int
	if err := e.conn.QueryRow(`SELECT COUNT(*) FROM system.auth_audit_log WHERE tenant_id = $1 AND event_type = $2`, ft.id, eventType).Scan(&n); err != nil {
		t.Fatalf("count audit rows: %v", err)
	}
	return n
}

type request struct {
	serve  func(http.ResponseWriter, *http.Request)
	method string
	path   string
	id     string
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
	if req.id != "" {
		r = r.WithContext(route.WithParams(r.Context(), map[string]string{"id": req.id}))
	}
	rec := httptest.NewRecorder()
	req.serve(rec, r)
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

func errorCode(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	return decode[struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}](t, rec).Error.Code
}

func (e *env) create(t *testing.T, ft fixtureTenant, token string, body map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	return do(t, ft, token, request{serve: e.handler.ServeCreate, method: http.MethodPost, path: "/admin/roles", body: body})
}

func (e *env) update(t *testing.T, ft fixtureTenant, token, id string, body map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	return do(t, ft, token, request{serve: e.handler.ServeUpdate, method: http.MethodPatch, path: "/admin/roles/" + id, id: id, body: body})
}

func (e *env) remove(t *testing.T, ft fixtureTenant, token, id string) *httptest.ResponseRecorder {
	t.Helper()
	return do(t, ft, token, request{serve: e.handler.ServeDelete, method: http.MethodDelete, path: "/admin/roles/" + id, id: id})
}

func (e *env) roleID(t *testing.T, ft fixtureTenant, name string) string {
	t.Helper()
	id, err := e.roles.GetRoleByName(t.Context(), ft.slug, name)
	if err != nil {
		t.Fatalf("GetRoleByName(%q) error: %v", name, err)
	}
	return id
}

func TestEveryRoute_RequiresTheAdminRole(t *testing.T) {
	e := newEnv(t)
	ft := e.newTenant(t)
	token := e.issue(t, ft, e.member(t, ft, "user"))
	id := e.roleID(t, ft, "user")

	for _, req := range []request{
		{serve: e.handler.ServeList, method: http.MethodGet, path: "/admin/roles"},
		{serve: e.handler.ServePermissions, method: http.MethodGet, path: "/admin/roles/permissions"},
		{serve: e.handler.ServeGet, method: http.MethodGet, path: "/admin/roles/" + id, id: id},
		{serve: e.handler.ServeCreate, method: http.MethodPost, path: "/admin/roles", body: map[string]any{"name": "sneaky"}},
		{serve: e.handler.ServeUpdate, method: http.MethodPatch, path: "/admin/roles/" + id, id: id, body: map[string]any{"name": "sneaky"}},
		{serve: e.handler.ServeDelete, method: http.MethodDelete, path: "/admin/roles/" + id, id: id},
	} {
		if rec := do(t, ft, token, req); rec.Code != http.StatusForbidden {
			t.Errorf("%s %s as non-admin: status = %d, want 403", req.method, req.path, rec.Code)
		}
		if rec := do(t, ft, "", req); rec.Code != http.StatusUnauthorized {
			t.Errorf("%s %s anonymous: status = %d, want 401", req.method, req.path, rec.Code)
		}
	}
	if _, err := e.roles.GetRoleByName(t.Context(), ft.slug, "sneaky"); err == nil {
		t.Error("a non-admin created a role")
	}
}

func TestServeList_BuiltInRolesAreImmutableWithUserCounts(t *testing.T) {
	e := newEnv(t)
	ft := e.newTenant(t)
	admin := e.member(t, ft, "admin")
	e.member(t, ft, "user")
	e.member(t, ft, "user")

	rec := do(t, ft, e.issue(t, ft, admin), request{serve: e.handler.ServeList, method: http.MethodGet, path: "/admin/roles"})
	if rec.Code != http.StatusOK {
		t.Fatalf("list status = %d, body = %s", rec.Code, rec.Body)
	}
	got := decode[struct {
		Data []roleJSON `json:"data"`
	}](t, rec).Data
	counts := map[string]int{}
	for _, r := range got {
		if !r.IsImmutable {
			t.Errorf("built-in role %s has is_immutable false", r.Name)
		}
		counts[r.Name] = r.UserCount
	}
	if want := map[string]int{"admin": 1, "user": 2, "portal": 0}; fmt.Sprint(counts) != fmt.Sprint(want) {
		t.Errorf("user counts = %v, want %v", counts, want)
	}
}

func TestServeCreate(t *testing.T) {
	e := newEnv(t)
	ft := e.newTenant(t)
	token := e.issue(t, ft, e.member(t, ft, "admin"))

	rec := e.create(t, ft, token, map[string]any{"name": "sales_rep", "description": "  Sells widgets ", "permissions": []string{permWrite, permRead, permRead}})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create status = %d, body = %s", rec.Code, rec.Body)
	}
	created := decode[roleDetailJSON](t, rec)
	if created.Name != "sales_rep" || created.Description == nil || *created.Description != "Sells widgets" || created.IsImmutable || !slices.Equal(created.Permissions, []string{permRead, permWrite}) {
		t.Errorf("created = %+v", created)
	}
	if n := e.auditCount(t, ft, "role.created"); n != 1 {
		t.Errorf("role.created rows = %d, want 1", n)
	}
	if _, ok := e.perms.Lookup(created.ID); !ok {
		t.Error("new role missing from the role permission map")
	}

	for _, c := range []struct {
		body     map[string]any
		status   int
		wantCode string
	}{
		{map[string]any{"name": "sales_rep"}, http.StatusConflict, "role_name_taken"},
		{map[string]any{"name": "admin"}, http.StatusConflict, "role_name_taken"},
		{map[string]any{"name": "Sales Rep"}, http.StatusBadRequest, "invalid_name"},
		{map[string]any{"name": ""}, http.StatusBadRequest, "invalid_name"},
		{map[string]any{"name": "superadmin"}, http.StatusBadRequest, "invalid_name"},
		{map[string]any{"name": "auditor", "permissions": []string{"nope:thing:read"}}, http.StatusBadRequest, "unknown_permission"},
	} {
		rec := e.create(t, ft, token, c.body)
		if rec.Code != c.status || errorCode(t, rec) != c.wantCode {
			t.Errorf("create %v = %d %s, want %d %s", c.body, rec.Code, rec.Body, c.status, c.wantCode)
		}
	}
	if n := e.auditCount(t, ft, "role.created"); n != 1 {
		t.Errorf("role.created rows after rejections = %d, want 1", n)
	}
}

func TestServeGet(t *testing.T) {
	e := newEnv(t)
	ft := e.newTenant(t)
	other := e.newTenant(t)
	token := e.issue(t, ft, e.member(t, ft, "admin"))
	created := decode[roleDetailJSON](t, e.create(t, ft, token, map[string]any{"name": "viewer", "permissions": []string{permRead}}))

	get := func(id string) *httptest.ResponseRecorder {
		return do(t, ft, token, request{serve: e.handler.ServeGet, method: http.MethodGet, path: "/admin/roles/" + id, id: id})
	}
	rec := get(created.ID)
	if got := decode[roleDetailJSON](t, rec); rec.Code != http.StatusOK || !slices.Equal(got.Permissions, []string{permRead}) {
		t.Errorf("get = %d %+v", rec.Code, got)
	}
	for _, id := range []string{"not-a-uuid", uuid.New().String(), e.roleID(t, other, "user")} {
		if rec := get(id); rec.Code != http.StatusNotFound {
			t.Errorf("GET /admin/roles/%s = %d, want 404", id, rec.Code)
		}
	}
}

func TestServeUpdate_PermissionChangeReachesSignedInHolders(t *testing.T) {
	e := newEnv(t)
	ft := e.newTenant(t)
	token := e.issue(t, ft, e.member(t, ft, "admin"))
	created := decode[roleDetailJSON](t, e.create(t, ft, token, map[string]any{"name": "clerk", "permissions": []string{permRead}}))
	holder := e.member(t, ft, "clerk")
	holderToken := e.issue(t, ft, holder)
	if !e.can(t, ft, holderToken, permRead) || e.can(t, ft, holderToken, permWrite) {
		t.Fatal("holder's starting permissions are wrong")
	}

	rec := e.update(t, ft, token, created.ID, map[string]any{"name": "senior_clerk", "description": "", "permissions": []string{permWrite}})
	if rec.Code != http.StatusOK {
		t.Fatalf("update status = %d, body = %s", rec.Code, rec.Body)
	}
	updated := decode[roleDetailJSON](t, rec)
	if updated.Name != "senior_clerk" || updated.Description != nil || !slices.Equal(updated.Permissions, []string{permWrite}) || updated.UserCount != 1 {
		t.Errorf("updated = %+v", updated)
	}
	if e.can(t, ft, holderToken, permRead) || !e.can(t, ft, holderToken, permWrite) {
		t.Error("the signed-in holder's permission set didn't follow the role change")
	}
	if n := e.auditCount(t, ft, "role.updated"); n != 1 {
		t.Errorf("role.updated rows = %d, want 1", n)
	}

	rec = e.update(t, ft, token, created.ID, map[string]any{"description": "Handles the books"})
	if got := decode[roleDetailJSON](t, rec); got.Description == nil || *got.Description != "Handles the books" || got.Name != "senior_clerk" || !slices.Equal(got.Permissions, []string{permWrite}) {
		t.Errorf("description-only update = %+v", got)
	}
}

func TestServeUpdate_Rejections(t *testing.T) {
	e := newEnv(t)
	ft := e.newTenant(t)
	token := e.issue(t, ft, e.member(t, ft, "admin"))
	created := decode[roleDetailJSON](t, e.create(t, ft, token, map[string]any{"name": "clerk"}))

	for _, c := range []struct {
		id       string
		body     map[string]any
		status   int
		wantCode string
	}{
		{e.roleID(t, ft, "user"), map[string]any{"permissions": []string{permRead}}, http.StatusForbidden, "role_immutable"},
		{e.roleID(t, ft, "admin"), map[string]any{"name": "boss"}, http.StatusForbidden, "role_immutable"},
		{created.ID, map[string]any{"name": "portal"}, http.StatusConflict, "role_name_taken"},
		{created.ID, map[string]any{"permissions": []string{"nope:x:y"}}, http.StatusBadRequest, "unknown_permission"},
		{created.ID, map[string]any{"name": "Bad Name"}, http.StatusBadRequest, "invalid_name"},
		{uuid.New().String(), map[string]any{"name": "ghost"}, http.StatusNotFound, "not_found"},
	} {
		rec := e.update(t, ft, token, c.id, c.body)
		if rec.Code != c.status || errorCode(t, rec) != c.wantCode {
			t.Errorf("update %s %v = %d %s, want %d %s", c.id, c.body, rec.Code, rec.Body, c.status, c.wantCode)
		}
	}
	if n := e.auditCount(t, ft, "role.updated"); n != 0 {
		t.Errorf("role.updated rows = %d, want 0", n)
	}
}

func TestServeDelete(t *testing.T) {
	e := newEnv(t)
	ft := e.newTenant(t)
	token := e.issue(t, ft, e.member(t, ft, "admin"))
	used := decode[roleDetailJSON](t, e.create(t, ft, token, map[string]any{"name": "used"}))
	unused := decode[roleDetailJSON](t, e.create(t, ft, token, map[string]any{"name": "unused"}))
	e.member(t, ft, "used")

	for _, c := range []struct {
		id       string
		status   int
		wantCode string
	}{
		{used.ID, http.StatusConflict, "role_in_use"},
		{e.roleID(t, ft, "portal"), http.StatusForbidden, "role_immutable"},
		{uuid.New().String(), http.StatusNotFound, "not_found"},
	} {
		rec := e.remove(t, ft, token, c.id)
		if rec.Code != c.status || errorCode(t, rec) != c.wantCode {
			t.Errorf("delete %s = %d %s, want %d %s", c.id, rec.Code, rec.Body, c.status, c.wantCode)
		}
	}

	if rec := e.remove(t, ft, token, unused.ID); rec.Code != http.StatusNoContent {
		t.Fatalf("delete unused = %d, body = %s", rec.Code, rec.Body)
	}
	if _, err := e.roles.GetRole(t.Context(), ft.slug, unused.ID); err == nil {
		t.Error("deleted role still exists")
	}
	if _, ok := e.perms.Lookup(unused.ID); ok {
		t.Error("deleted role still in the role permission map")
	}
	if n := e.auditCount(t, ft, "role.deleted"); n != 1 {
		t.Errorf("role.deleted rows = %d, want 1", n)
	}
}

func TestServeDelete_AnExpiredGrantDoesNotBlock(t *testing.T) {
	e := newEnv(t)
	ft := e.newTenant(t)
	token := e.issue(t, ft, e.member(t, ft, "admin"))
	lapsed := decode[roleDetailJSON](t, e.create(t, ft, token, map[string]any{"name": "lapsed"}))
	holder := e.member(t, ft, "lapsed")
	if _, err := e.conn.Exec(fmt.Sprintf(`UPDATE %s.user_roles SET expires_at = NOW() - INTERVAL '1 hour' WHERE user_id = $1 AND role_id = $2`, tenantschema.Name(ft.slug)), holder, lapsed.ID); err != nil {
		t.Fatalf("expire grant: %v", err)
	}
	if rec := e.remove(t, ft, token, lapsed.ID); rec.Code != http.StatusNoContent {
		t.Errorf("delete with only an expired grant = %d, body = %s", rec.Code, rec.Body)
	}
}

func TestServeDelete_APendingInvitationBlocks(t *testing.T) {
	e := newEnv(t)
	ft := e.newTenant(t)
	token := e.issue(t, ft, e.member(t, ft, "admin"))
	offered := decode[roleDetailJSON](t, e.create(t, ft, token, map[string]any{"name": "offered"}))
	email := fmt.Sprintf("invitee%d@example.com", time.Now().UnixNano())
	t.Cleanup(func() { _, _ = e.conn.Exec(`DELETE FROM system.users WHERE email = $1`, email) })
	inv, err := e.invites.Invite(t.Context(), ft.slug, email, "offered", "", nil)
	if err != nil {
		t.Fatalf("Invite() error: %v", err)
	}

	if rec := e.remove(t, ft, token, offered.ID); rec.Code != http.StatusConflict || errorCode(t, rec) != "role_in_use" {
		t.Errorf("delete with a pending invitation = %d %s, want 409 role_in_use", rec.Code, rec.Body)
	}
	if err := e.invites.Revoke(t.Context(), ft.slug, inv.ID, nil); err != nil {
		t.Fatalf("Revoke() error: %v", err)
	}
	if rec := e.remove(t, ft, token, offered.ID); rec.Code != http.StatusNoContent {
		t.Errorf("delete after revoking the invitation = %d %s, want 204", rec.Code, rec.Body)
	}
}

func TestWrites_OnlyGrantTheTenantsCatalog(t *testing.T) {
	e := newEnv(t)
	ft := e.newTenant(t)
	token := e.issue(t, ft, e.member(t, ft, "admin"))

	rec := e.create(t, ft, token, map[string]any{"name": "gadgeteer", "permissions": []string{permHidden}})
	if rec.Code != http.StatusBadRequest || errorCode(t, rec) != "unknown_permission" {
		t.Errorf("create with a disabled module's permission = %d %s, want 400 unknown_permission", rec.Code, rec.Body)
	}

	created := decode[roleDetailJSON](t, e.create(t, ft, token, map[string]any{"name": "keeper"}))
	if _, err := e.conn.Exec(fmt.Sprintf(`INSERT INTO %s.role_permissions (role_id, permission_name) VALUES ($1, $2)`, tenantschema.Name(ft.slug)), created.ID, permHidden); err != nil {
		t.Fatalf("grant held permission: %v", err)
	}
	rec = e.update(t, ft, token, created.ID, map[string]any{"permissions": []string{permHidden, permRead}})
	if rec.Code != http.StatusOK {
		t.Errorf("update keeping a permission the role already holds = %d %s, want 200", rec.Code, rec.Body)
	}
}

func TestServeCreate_DescriptionLimitCountsCharacters(t *testing.T) {
	e := newEnv(t)
	ft := e.newTenant(t)
	token := e.issue(t, ft, e.member(t, ft, "admin"))

	if rec := e.create(t, ft, token, map[string]any{"name": "wide", "description": strings.Repeat("役", 300)}); rec.Code != http.StatusCreated {
		t.Errorf("300 multibyte characters = %d %s, want 201", rec.Code, rec.Body)
	}
	rec := e.create(t, ft, token, map[string]any{"name": "wider", "description": strings.Repeat("a", 501)})
	if rec.Code != http.StatusBadRequest || errorCode(t, rec) != "invalid_description" {
		t.Errorf("501 characters = %d %s, want 400 invalid_description", rec.Code, rec.Body)
	}
}

func TestListener_CatchesUpOnChangesMadeBeforeItListened(t *testing.T) {
	e := newEnv(t)
	ft := e.newTenant(t)
	token := e.issue(t, ft, e.member(t, ft, "admin"))
	created := decode[roleDetailJSON](t, e.create(t, ft, token, map[string]any{"name": "early", "permissions": []string{permRead}}))

	replica := permcache.NewRolePermissionMap()
	listener := permcache.NewListener(e.conn, e.tenants, e.roles, e.registry, replica)
	ready := make(chan struct{})
	listener.Start(t.Context(), func() { close(ready) })
	t.Cleanup(listener.Stop)
	<-ready

	idx, _ := e.registry().Index(permRead)
	if bits, ok := replica.Lookup(created.ID); !ok || !bits.Has(idx) {
		t.Error("a role created before the listener started is missing once it's listening")
	}
}

func TestServePermissions_ListsTheTenantsEnabledModules(t *testing.T) {
	e := newEnv(t)
	ft := e.newTenant(t)
	token := e.issue(t, ft, e.member(t, ft, "admin"))

	rec := do(t, ft, token, request{serve: e.handler.ServePermissions, method: http.MethodGet, path: "/admin/roles/permissions"})
	got := decode[struct {
		Permissions []catalogEntryJSON `json:"permissions"`
	}](t, rec).Permissions
	want := []catalogEntryJSON{
		{Name: permRead, Description: "View widgets", Category: "Widgets", Module: "rolestest"},
		{Name: permWrite, Description: "Edit widgets", Category: "Widgets", Module: "rolestest"},
	}
	if rec.Code != http.StatusOK || !slices.Equal(got, want) {
		t.Errorf("catalog = %d %+v, want %+v", rec.Code, got, want)
	}
}

func TestRoleChanges_ReachAnotherReplica(t *testing.T) {
	e := newEnv(t)
	ft := e.newTenant(t)
	token := e.issue(t, ft, e.member(t, ft, "admin"))

	replica := permcache.NewRolePermissionMap()
	listener := permcache.NewListener(e.conn, e.tenants, e.roles, e.registry, replica)
	ready := make(chan struct{})
	listener.Start(t.Context(), func() { close(ready) })
	t.Cleanup(listener.Stop)
	<-ready

	created := decode[roleDetailJSON](t, e.create(t, ft, token, map[string]any{"name": "remote", "permissions": []string{permRead}}))
	idx, _ := e.registry().Index(permRead)
	waitFor(t, "the other replica to learn the new role", func() bool {
		bits, ok := replica.Lookup(created.ID)
		return ok && bits.Has(idx)
	})

	e.update(t, ft, token, created.ID, map[string]any{"permissions": []string{}})
	waitFor(t, "the other replica to drop the permission", func() bool {
		bits, ok := replica.Lookup(created.ID)
		return ok && !bits.Has(idx)
	})

	e.remove(t, ft, token, created.ID)
	waitFor(t, "the other replica to drop the role", func() bool {
		_, ok := replica.Lookup(created.ID)
		return !ok
	})
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
