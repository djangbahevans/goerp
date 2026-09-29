package connectorprimary

import (
	"context"
	"database/sql"
	"encoding/json/v2"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
	"uuid"

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
	"github.com/djangbahevans/goerp/internal/engine/providerselect"
	"github.com/djangbahevans/goerp/internal/engine/role"
	"github.com/djangbahevans/goerp/internal/engine/route"
	"github.com/djangbahevans/goerp/internal/engine/secrets"
	"github.com/djangbahevans/goerp/internal/engine/tenant"
	tenantresolve "github.com/djangbahevans/goerp/internal/engine/tenant/resolve"
	"github.com/djangbahevans/goerp/internal/engine/tenantschema"
	"github.com/djangbahevans/goerp/internal/engine/user"
)

const localPostgresDSN = "postgres://goerp:dev@localhost:15432/goerp"

// spyAudit records Emit calls, same shape as planchange's own spyAudit.
type spyAudit struct {
	mu     sync.Mutex
	events []map[string]any
}

func (a *spyAudit) Emit(ctx context.Context, tenantSlug, eventName, userID, actorUserID string, payload map[string]any) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.events = append(a.events, map[string]any{"tenant": tenantSlug, "event": eventName, "user_id": userID, "actor_user_id": actorUserID, "payload": payload})
	return nil
}

func (a *spyAudit) count() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.events)
}

type fixture struct {
	handler    *Handler
	issuer     *authtoken.Issuer
	selection  *providerselect.Store
	audit      *spyAudit
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
	selection := providerselect.NewStore(conn)
	if err := selection.Bootstrap(ctx); err != nil {
		t.Fatalf("providerselect Bootstrap() error: %v", err)
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
	audit := &spyAudit{}
	handler := NewHandler(tenantResolver, authChecker, selection, audit)

	slug := fmt.Sprintf("connprimarytest%d", time.Now().UnixNano())
	tt, err := tenantStore.CreateTenant(ctx, slug, "Connector Primary Test Co")
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
		selection:  selection,
		audit:      audit,
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

// createUserWithRole creates an active user holding roleName in the
// fixture's tenant, mirroring planchange's own helper.
func (f *fixture) createUserWithRole(t *testing.T, roleName string) string {
	t.Helper()
	ctx := t.Context()

	email := fmt.Sprintf("connprimarytest%d@example.com", time.Now().UnixNano())
	userID, err := user.NewStore(f.conn).FindOrCreateInvited(ctx, email)
	if err != nil {
		t.Fatalf("FindOrCreateInvited() error: %v", err)
	}
	t.Cleanup(func() { _, _ = f.conn.Exec(`DELETE FROM system.users WHERE id = $1`, userID) })
	if _, err := f.conn.Exec(`UPDATE system.users SET status = 'active' WHERE id = $1`, userID); err != nil {
		t.Fatalf("activate fixture user: %v", err)
	}
	t.Cleanup(func() { _, _ = f.conn.Exec(`DELETE FROM system.sessions WHERE user_id = $1`, userID) })

	roleID, err := role.NewStore(f.conn).GetRoleByName(ctx, f.tenantSlug, roleName)
	if err != nil {
		t.Fatalf("GetRoleByName(%q) error: %v", roleName, err)
	}
	schema := tenantschema.Name(f.tenantSlug)
	if _, err := f.conn.Exec(fmt.Sprintf("WITH m AS (INSERT INTO %[1]s.tenant_members (user_id) VALUES ($1) ON CONFLICT DO NOTHING) INSERT INTO %[1]s.user_roles (user_id, role_id) VALUES ($1, $2)", schema), userID, roleID); err != nil {
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

func (f *fixture) install(t *testing.T, moduleName, category string) {
	t.Helper()
	if _, err := f.conn.Exec(`
		INSERT INTO system.tenant_module_settings (tenant_id, module_name, enabled, provider_category)
		VALUES ($1, $2, true, $3)
	`, f.tenantID, moduleName, category); err != nil {
		t.Fatalf("install %s: %v", moduleName, err)
	}
}

func (f *fixture) doSetPrimary(t *testing.T, accessToken, moduleName string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPatch, "/admin/connectors/"+moduleName+"/set-primary", nil)
	req = req.WithContext(route.WithParams(req.Context(), map[string]string{"name": moduleName}))
	req.Host = f.domain
	req.RemoteAddr = "203.0.113.7:54321"
	if accessToken != "" {
		req.Header.Set("Authorization", "Bearer "+accessToken)
	}
	rec := httptest.NewRecorder()
	f.handler.ServeSetPrimary(rec, req)
	return rec
}

func errorCode(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode error body %q: %v", rec.Body.String(), err)
	}
	return body.Error.Code
}

func TestServeSetPrimary_SwitchesProviderImmediately(t *testing.T) {
	f := newFixture(t)
	adminID := f.createUserWithRole(t, "admin")
	token := f.issueAccessToken(t, adminID)
	f.install(t, "connector_twilio", providerselect.CategorySMS)
	f.install(t, "connector_africastalking", providerselect.CategorySMS)

	for _, module := range []string{"connector_africastalking", "connector_twilio"} {
		rec := f.doSetPrimary(t, token, module)
		if rec.Code != http.StatusOK {
			t.Fatalf("set-primary %s status = %d, body = %s", module, rec.Code, rec.Body.String())
		}
		var body struct {
			ModuleName string `json:"module_name"`
			Category   string `json:"category"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		if body.ModuleName != module || body.Category != providerselect.CategorySMS {
			t.Fatalf("body = %+v", body)
		}

		got, err := f.selection.Resolve(t.Context(), f.tenantID, providerselect.CategorySMS)
		if err != nil || got != module {
			t.Fatalf("Resolve() after set-primary %s = %q, %v", module, got, err)
		}
	}
	if n := f.audit.count(); n != 2 {
		t.Fatalf("audit events = %d, want 2", n)
	}
	for _, e := range f.audit.events {
		if e["user_id"] != "" || e["actor_user_id"] != adminID {
			t.Errorf("%s user_id/actor_user_id = %q/%q, want \"\"/%q", e["event"], e["user_id"], e["actor_user_id"], adminID)
		}
		if _, ok := e["payload"].(map[string]any)["performed_by"]; ok {
			t.Errorf("%s payload contains performed_by", e["event"])
		}
	}
}

func TestServeSetPrimary_RejectsPaymentProvider(t *testing.T) {
	f := newFixture(t)
	token := f.issueAccessToken(t, f.createUserWithRole(t, "admin"))
	f.install(t, "connector_paystack", providerselect.CategoryPayment)

	rec := f.doSetPrimary(t, token, "connector_paystack")
	if rec.Code != http.StatusUnprocessableEntity || errorCode(t, rec) != "multi_active_category" {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var n int
	if err := f.conn.QueryRow(`SELECT count(*) FROM system.tenant_provider_selections WHERE tenant_id = $1`, f.tenantID).Scan(&n); err != nil {
		t.Fatalf("count selections: %v", err)
	}
	if n != 0 {
		t.Fatalf("selection rows = %d, want 0", n)
	}
}

func TestServeSetPrimary_UnknownConnectorIsNotFound(t *testing.T) {
	f := newFixture(t)
	token := f.issueAccessToken(t, f.createUserWithRole(t, "admin"))

	rec := f.doSetPrimary(t, token, "connector_missing")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

func TestServeSetPrimary_RequiresAdmin(t *testing.T) {
	f := newFixture(t)
	f.install(t, "connector_twilio", providerselect.CategorySMS)

	if rec := f.doSetPrimary(t, "", "connector_twilio"); rec.Code != http.StatusUnauthorized {
		t.Fatalf("no token: status = %d, want 401", rec.Code)
	}
	token := f.issueAccessToken(t, f.createUserWithRole(t, "user"))
	if rec := f.doSetPrimary(t, token, "connector_twilio"); rec.Code != http.StatusForbidden {
		t.Fatalf("non-admin: status = %d, want 403", rec.Code)
	}
	if n := f.audit.count(); n != 0 {
		t.Fatalf("audit events = %d, want 0", n)
	}
}
