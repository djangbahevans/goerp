package adminsettings

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json/v2"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"mime/multipart"
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
	"github.com/djangbahevans/goerp/internal/engine/auth/ipallowlist"
	"github.com/djangbahevans/goerp/internal/engine/auth/password"
	"github.com/djangbahevans/goerp/internal/engine/auth/rowcrypt"
	"github.com/djangbahevans/goerp/internal/engine/auth/session"
	"github.com/djangbahevans/goerp/internal/engine/auth/sessionpolicy"
	"github.com/djangbahevans/goerp/internal/engine/auth/sessionrevoke"
	"github.com/djangbahevans/goerp/internal/engine/auth/signingkey"
	"github.com/djangbahevans/goerp/internal/engine/authaudit"
	"github.com/djangbahevans/goerp/internal/engine/authaudit/audittest"
	"github.com/djangbahevans/goerp/internal/engine/billing"
	"github.com/djangbahevans/goerp/internal/engine/cache"
	"github.com/djangbahevans/goerp/internal/engine/db"
	"github.com/djangbahevans/goerp/internal/engine/files"
	"github.com/djangbahevans/goerp/internal/engine/l10n/tenantl10n"
	"github.com/djangbahevans/goerp/internal/engine/mfa"
	"github.com/djangbahevans/goerp/internal/engine/mfa/enforce"
	"github.com/djangbahevans/goerp/internal/engine/notifconfig"
	"github.com/djangbahevans/goerp/internal/engine/notifications"
	"github.com/djangbahevans/goerp/internal/engine/notify"
	"github.com/djangbahevans/goerp/internal/engine/permcache"
	"github.com/djangbahevans/goerp/internal/engine/permission"
	"github.com/djangbahevans/goerp/internal/engine/registry"
	"github.com/djangbahevans/goerp/internal/engine/role"
	"github.com/djangbahevans/goerp/internal/engine/secrets"
	"github.com/djangbahevans/goerp/internal/engine/storage"
	"github.com/djangbahevans/goerp/internal/engine/tenant"
	tenantresolve "github.com/djangbahevans/goerp/internal/engine/tenant/resolve"
	"github.com/djangbahevans/goerp/internal/engine/tenantconfig"
	"github.com/djangbahevans/goerp/internal/engine/tenantschema"
	"github.com/djangbahevans/goerp/internal/engine/user"
)

const localPostgresDSN = "postgres://goerp:dev@localhost:15432/goerp"

// clientIP is the address every test request comes from.
const clientIP = "203.0.113.7"

var platformLocales = []string{"en", "fr", "ar"}

type env struct {
	conn     *sql.DB
	tenants  *tenant.Store
	users    *user.Store
	roles    *role.Store
	files    *files.Store
	issuer   *authtoken.Issuer
	checker  *authcheck.Checker
	perms    *permcache.RolePermissionMap
	handler  *Handler
	sessions *sessionpolicy.Store
	billing  *billing.Store
	config   *tenantconfig.Store
	modules  *registry.ModuleRegistry
	notif    *notifconfig.Service
	notifs   *notifications.Store
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
	configStore := tenantconfig.NewStore(conn)
	mfaStore := mfa.NewStore(conn)
	for _, b := range []struct {
		name      string
		bootstrap func(context.Context) error
	}{
		{"tenant", tenantStore.Bootstrap}, {"user", userStore.Bootstrap}, {"session", sessionStore.Bootstrap},
		{"apikey", apiKeys.Bootstrap}, {"billing", billingStore.Bootstrap}, {"authaudit", auditStore.Bootstrap},
		{"signingkey", signingKeyStore.Bootstrap}, {"tenantconfig", configStore.Bootstrap}, {"mfa", mfaStore.Bootstrap},
	} {
		if err := b.bootstrap(ctx); err != nil {
			t.Fatalf("%s Bootstrap() error: %v", b.name, err)
		}
	}
	signingKeySet, err := signingKeyStore.LoadOrGenerate(ctx)
	if err != nil {
		t.Fatalf("signingkey LoadOrGenerate() error: %v", err)
	}

	t.Setenv("GOERP_STORAGE_LOCAL_DIR", t.TempDir())
	backend, err := storage.New("local")
	if err != nil {
		t.Fatalf("storage.New() error: %v", err)
	}

	roleStore := role.NewStore(conn)
	perms := permcache.NewRolePermissionMap()
	revoker := sessionrevoke.NewRevoker(sessionStore, cacheClient)
	mfaPolicies := enforce.NewStore(configStore)
	checker := authcheck.NewChecker(&signingKeySet.Active, revoker, userStore, roleStore, permcache.NewRoleCache(cacheClient), perms, apiKeys, false, nil, mfaStore, mfaPolicies)
	sessions := sessionpolicy.NewStore(configStore)
	filesStore := files.NewStore(conn)
	modules := &registry.ModuleRegistry{}
	rowKeys := &rowcrypt.RowKeySet{Active: rowcrypt.RowKey{KeyID: "test-key", Key: make([]byte, 32)}}
	notif := notifconfig.NewService(tenantconfig.NewResolver(configStore, tenantStore, modules), configStore, rowKeys)
	notifs := notifications.NewStore(conn)

	return &env{
		conn:     conn,
		tenants:  tenantStore,
		users:    userStore,
		roles:    roleStore,
		files:    filesStore,
		issuer:   authtoken.NewIssuer(&signingKeySet.Active, tenantStore, roleStore, sessionStore),
		checker:  checker,
		perms:    perms,
		sessions: sessions,
		billing:  billingStore,
		config:   configStore,
		modules:  modules,
		notif:    notif,
		notifs:   notifs,
		handler: NewHandler(Deps{
			Tenants:      tenantresolve.NewResolver(tenantStore, cacheClient, billingStore),
			Auth:         checker,
			TenantStore:  tenantStore,
			Cache:        cacheClient,
			Roles:        roleStore,
			Config:       configStore,
			MFA:          mfaPolicies,
			Passwords:    password.NewPolicyStore(configStore, role.NewStore(conn)),
			Sessions:     sessions,
			IPAllowlists: ipallowlist.NewStore(configStore),
			Locales:      tenantl10n.NewStore(configStore, platformLocales),
			Audit:        auditStore,
			Storage:      backend,
			Files:        filesStore,
			MaxLogoBytes: MaxLogoBytes,

			Notifications: notif,
			Registry:      modules,
			Users:         userStore,
			TestEmail:     &notify.EmailTester{Registry: modules, Tenants: tenantStore, AppBaseURL: "http://localhost:5173", PlatformDomain: "goerp.test"},

			Templates:      notifs,
			AppBaseURL:     "http://localhost:5173",
			PlatformDomain: "goerp.test",
		}),
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

	slug := fmt.Sprintf("adminsettingstest%d", time.Now().UnixNano())
	tt, err := e.tenants.CreateTenant(ctx, slug, "Settings Test Co")
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
	if _, err := e.conn.Exec(`
		CREATE TABLE ` + schema + `.module_config (
		    module_name TEXT NOT NULL,
		    key         TEXT NOT NULL,
		    value       JSONB NOT NULL,
		    value_type  TEXT NOT NULL,
		    encrypted   BOOLEAN NOT NULL DEFAULT FALSE,
		    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
		    updated_by  UUID,
		    PRIMARY KEY (module_name, key)
		)`); err != nil {
		t.Fatalf("create module_config table: %v", err)
	}
	if err := e.files.Bootstrap(ctx, slug); err != nil {
		t.Fatalf("files Bootstrap() error: %v", err)
	}
	registry := func() *permission.PermissionRegistry { return permission.NewPermissionRegistry() }
	if err := e.perms.RebuildTenant(ctx, e.roles, registry, slug); err != nil {
		t.Fatalf("RebuildTenant() error: %v", err)
	}
	return fixtureTenant{id: tt.ID, slug: slug, domain: domain}
}

// member creates an active user holding roleName in ft and returns an
// access token for them.
func (e *env) member(t *testing.T, ft fixtureTenant, roleName string) (userID, token string) {
	t.Helper()
	ctx := t.Context()
	id, err := e.users.FindOrCreateInvited(ctx, fmt.Sprintf("adminsettings%d@example.com", time.Now().UnixNano()))
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
	return id, tokens.AccessToken
}

func (e *env) do(t *testing.T, ft fixtureTenant, token string, handler http.HandlerFunc, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var reader *bytes.Reader
	if raw, ok := body.(string); ok {
		reader = bytes.NewReader([]byte(raw))
	} else {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal body: %v", err)
		}
		reader = bytes.NewReader(b)
	}
	req := httptest.NewRequest(method, path, reader)
	req.Host = ft.domain
	req.RemoteAddr = clientIP + ":44321"
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	handler(rec, req)
	return rec
}

func (e *env) get(t *testing.T, ft fixtureTenant, token string) Settings {
	t.Helper()
	rec := e.do(t, ft, token, e.handler.ServeGet, http.MethodGet, "/admin/settings", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET status = %d, body = %s", rec.Code, rec.Body.String())
	}
	return decode[Settings](t, rec)
}

func (e *env) patch(t *testing.T, ft fixtureTenant, token string, body any) *httptest.ResponseRecorder {
	t.Helper()
	return e.do(t, ft, token, e.handler.ServePatch, http.MethodPatch, "/admin/settings", body)
}

func decode[T any](t *testing.T, rec *httptest.ResponseRecorder) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
		t.Fatalf("decode %s: %v", rec.Body.String(), err)
	}
	return v
}

type errorBody struct {
	Error struct {
		Code    string            `json:"code"`
		Details map[string]string `json:"details"`
	} `json:"error"`
}

func TestGet_Defaults(t *testing.T) {
	e := newEnv(t)
	ft := e.newTenant(t)
	_, token := e.member(t, ft, "admin")

	got := e.get(t, ft, token)
	want := Settings{
		General: General{Profile: tenant.Profile{Name: "Settings Test Co"}, DefaultLocale: "en", DefaultTimezone: "UTC"},
		Security: Security{
			MFA:               MFA{Mode: "optional", RequiredRoles: []string{}, MaxAssuranceAgeHours: 24},
			PasswordPolicy:    PasswordPolicy{MinLength: password.Global.MinLength, Enforcement: "nudge", GraceDays: password.DefaultGraceDays},
			EmailVerification: EmailVerification{Policy: VerificationTenantChoice, Required: true},
		},
		Localisation: Localisation{PlatformLocales: platformLocales, AvailableLocales: platformLocales, FirstDayOfWeek: "monday", NumberFormat: "1,234.56"},
	}
	gotJSON, _ := json.Marshal(got)
	wantJSON, _ := json.Marshal(want)
	if !bytes.Equal(gotJSON, wantJSON) {
		t.Errorf("GET =\n%s\nwant\n%s", gotJSON, wantJSON)
	}
}

func TestAdminRoleRequired(t *testing.T) {
	e := newEnv(t)
	ft := e.newTenant(t)
	_, token := e.member(t, ft, "user")

	if rec := e.do(t, ft, token, e.handler.ServeGet, http.MethodGet, "/admin/settings", ""); rec.Code != http.StatusForbidden {
		t.Errorf("GET status = %d, want 403", rec.Code)
	}
	if rec := e.patch(t, ft, token, map[string]any{"general": map[string]any{"name": "x"}}); rec.Code != http.StatusForbidden {
		t.Errorf("PATCH status = %d, want 403", rec.Code)
	}
	if rec := e.do(t, ft, "", e.handler.ServeGet, http.MethodGet, "/admin/settings", ""); rec.Code != http.StatusUnauthorized {
		t.Errorf("anonymous GET status = %d, want 401", rec.Code)
	}
}

func TestPatch_EveryFieldRoundTrips(t *testing.T) {
	e := newEnv(t)
	ft := e.newTenant(t)
	admin, token := e.member(t, ft, "admin")

	rec := e.patch(t, ft, token, map[string]any{
		"general": map[string]any{
			"name": "  Acme Ghana Ltd ", "address": "1 Liberation Rd\nAccra", "website": "https://acme.example",
			"country": "gh", "default_currency": "GHS", "default_locale": "fr", "default_timezone": "Africa/Accra",
		},
		"security": map[string]any{
			"mfa":                map[string]any{"mode": "required_for_roles", "required_roles": []string{"admin"}, "max_assurance_age_hours": 8},
			"password_policy":    map[string]any{"min_length": 16, "enforcement": "require", "grace_days": 30},
			"session":            map[string]any{"idle_timeout_minutes": 30, "absolute_max_minutes": 720},
			"ip_allowlist":       "203.0.113.0/24, 2001:db8::/32",
			"email_verification": map[string]any{"required": false},
		},
		"localisation": map[string]any{"available_locales": []string{"fr", "en"}, "first_day_of_week": "sunday", "number_format": "1.234,56"},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("PATCH status = %d, body = %s", rec.Code, rec.Body.String())
	}
	patched := decode[Settings](t, rec)
	changedAt := patched.Security.PasswordPolicy.ChangedAt
	if changedAt == nil || time.Since(*changedAt) > time.Minute {
		t.Errorf("password_policy.changed_at = %v, want the time of this PATCH", changedAt)
	}

	want := Settings{
		General: General{
			Profile: tenant.Profile{
				Name: "Acme Ghana Ltd", Address: new("1 Liberation Rd\nAccra"), Website: new("https://acme.example"),
				Country: new("GH"), DefaultCurrency: new("GHS"),
			},
			DefaultLocale: "fr", DefaultTimezone: "Africa/Accra",
		},
		Security: Security{
			MFA:               MFA{Mode: "required_for_roles", RequiredRoles: []string{"admin"}, MaxAssuranceAgeHours: 8},
			PasswordPolicy:    PasswordPolicy{MinLength: 16, Enforcement: "require", GraceDays: 30, ChangedAt: changedAt},
			Session:           Session{IdleTimeoutMinutes: 30, AbsoluteMaxMinutes: 720},
			IPAllowlist:       "203.0.113.0/24,2001:db8::/32",
			EmailVerification: EmailVerification{Policy: VerificationTenantChoice},
		},
		Localisation: Localisation{PlatformLocales: platformLocales, AvailableLocales: []string{"fr", "en"}, FirstDayOfWeek: "sunday", NumberFormat: "1.234,56"},
	}
	wantJSON, _ := json.Marshal(want)
	for name, got := range map[string]Settings{"PATCH response": patched, "GET after PATCH": e.get(t, ft, token)} {
		gotJSON, _ := json.Marshal(got)
		if !bytes.Equal(gotJSON, wantJSON) {
			t.Errorf("%s =\n%s\nwant\n%s", name, gotJSON, wantJSON)
		}
	}

	// The enforcing stores see what was written.
	policy, err := e.sessions.Load(t.Context(), ft.id)
	if err != nil || policy.IdleTimeout != 30*time.Minute || policy.AbsoluteMax != 12*time.Hour {
		t.Errorf("sessionpolicy Load() = %+v, %v", policy, err)
	}
	var n int
	if err := e.conn.QueryRow(`SELECT COUNT(*) FROM system.auth_audit_log WHERE tenant_id = $1 AND event_type = 'tenant.settings_updated'`, ft.id).Scan(&n); err != nil || n != 1 {
		t.Errorf("tenant.settings_updated audit rows = %d, %v, want 1", n, err)
	}
	audittest.AssertLatest(t, e.conn, ft.id, "tenant.settings_updated", "", admin)
}

func TestPatch_OmittedFieldsKeepTheirValues(t *testing.T) {
	e := newEnv(t)
	ft := e.newTenant(t)
	_, token := e.member(t, ft, "admin")

	if rec := e.patch(t, ft, token, map[string]any{
		"general":  map[string]any{"website": "https://acme.example", "default_currency": "USD"},
		"security": map[string]any{"password_policy": map[string]any{"min_length": 14}},
	}); rec.Code != http.StatusOK {
		t.Fatalf("first PATCH status = %d, body = %s", rec.Code, rec.Body.String())
	}
	rec := e.patch(t, ft, token, map[string]any{
		"general":  map[string]any{"website": ""},
		"security": map[string]any{"password_policy": map[string]any{"grace_days": 30}},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("second PATCH status = %d, body = %s", rec.Code, rec.Body.String())
	}
	got := decode[Settings](t, rec)
	if got.General.Website != nil {
		t.Errorf("website = %q, want cleared", *got.General.Website)
	}
	if got.General.DefaultCurrency == nil || *got.General.DefaultCurrency != "USD" {
		t.Errorf("default_currency = %v, want USD kept", got.General.DefaultCurrency)
	}
	if pp := got.Security.PasswordPolicy; pp.MinLength != 14 || pp.Enforcement != "nudge" || pp.GraceDays != 30 {
		t.Errorf("password_policy = %+v, want min_length 14 and enforcement kept, grace_days set", pp)
	}
}

func TestPatch_RejectsInvalidValues(t *testing.T) {
	e := newEnv(t)
	ft := e.newTenant(t)
	_, token := e.member(t, ft, "admin")

	cases := []struct {
		name  string
		body  map[string]any
		field string
	}{
		{"blank name", map[string]any{"general": map[string]any{"name": " "}}, "general.name"},
		{"website scheme", map[string]any{"general": map[string]any{"website": "javascript:alert(1)"}}, "general.website"},
		{"country", map[string]any{"general": map[string]any{"country": "Ghana"}}, "general.country"},
		{"currency", map[string]any{"general": map[string]any{"default_currency": "cedi"}}, "general.default_currency"},
		{"mfa mode", map[string]any{"security": map[string]any{"mfa": map[string]any{"mode": "sometimes"}}}, "security.mfa.mode"},
		{"unknown role", map[string]any{"security": map[string]any{"mfa": map[string]any{"mode": "required_for_roles", "required_roles": []string{"nope"}}}}, "security.mfa.required_roles"},
		{"no roles", map[string]any{"security": map[string]any{"mfa": map[string]any{"mode": "required_for_roles"}}}, "security.mfa.required_roles"},
		{"roles without the mode", map[string]any{"security": map[string]any{"mfa": map[string]any{"mode": "required", "required_roles": []string{"admin"}}}}, "security.mfa.required_roles"},
		{"min length below the platform", map[string]any{"security": map[string]any{"password_policy": map[string]any{"min_length": 6}}}, "security.password_policy.min_length"},
		{"min length above the tenant maximum", map[string]any{"security": map[string]any{"password_policy": map[string]any{"min_length": 21}}}, "security.password_policy.min_length"},
		{"enforcement", map[string]any{"security": map[string]any{"password_policy": map[string]any{"enforcement": "block"}}}, "security.password_policy.enforcement"},
		{"grace days", map[string]any{"security": map[string]any{"password_policy": map[string]any{"grace_days": 91}}}, "security.password_policy.grace_days"},
		{"idle too short", map[string]any{"security": map[string]any{"session": map[string]any{"idle_timeout_minutes": 5}}}, "security.session"},
		{"absolute below idle", map[string]any{"security": map[string]any{"session": map[string]any{"idle_timeout_minutes": 120, "absolute_max_minutes": 60}}}, "security.session"},
		{"bad cidr", map[string]any{"security": map[string]any{"ip_allowlist": "10.0.0.0/33"}}, "security.ip_allowlist"},
		{"allowlist without the caller", map[string]any{"security": map[string]any{"ip_allowlist": "198.51.100.0/24"}}, "security.ip_allowlist"},
		{"default locale not available", map[string]any{"general": map[string]any{"default_locale": "de"}}, "general.default_locale"},
		{"default locale outside the new list", map[string]any{"general": map[string]any{"default_locale": "fr"}, "localisation": map[string]any{"available_locales": []string{"en"}}}, "general.default_locale"},
		{"list drops the default locale", map[string]any{"localisation": map[string]any{"available_locales": []string{"fr"}}}, "localisation.available_locales"},
		{"timezone", map[string]any{"general": map[string]any{"default_timezone": "Mars/Olympus"}}, "general.default_timezone"},
		{"unknown locale", map[string]any{"localisation": map[string]any{"available_locales": []string{"en", "de"}}}, "localisation.available_locales"},
		{"no locales", map[string]any{"localisation": map[string]any{"available_locales": []string{}}}, "localisation.available_locales"},
		{"first day", map[string]any{"localisation": map[string]any{"first_day_of_week": "friday"}}, "localisation.first_day_of_week"},
		{"number format", map[string]any{"localisation": map[string]any{"number_format": "1234.56"}}, "localisation.number_format"},
	}
	before := e.get(t, ft, token)
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rec := e.patch(t, ft, token, c.body)
			if rec.Code != http.StatusUnprocessableEntity {
				t.Fatalf("status = %d, body = %s, want 422", rec.Code, rec.Body.String())
			}
			if got := decode[errorBody](t, rec); got.Error.Code != "invalid_setting" || got.Error.Details["field"] != c.field {
				t.Errorf("error = %+v, want invalid_setting on %s", got.Error, c.field)
			}
		})
	}
	// A request that fails validation writes nothing, even the valid
	// parts of it.
	e.patch(t, ft, token, map[string]any{"general": map[string]any{"name": "Changed", "country": "Ghana"}})
	afterJSON, _ := json.Marshal(e.get(t, ft, token))
	beforeJSON, _ := json.Marshal(before)
	if !bytes.Equal(afterJSON, beforeJSON) {
		t.Errorf("settings changed by rejected requests:\n%s\nwant\n%s", afterJSON, beforeJSON)
	}

	if rec := e.patch(t, ft, token, map[string]any{"general": map[string]any{"logo_url": "https://evil.example/x.png"}}); rec.Code != http.StatusBadRequest {
		t.Errorf("unknown field status = %d, want 400", rec.Code)
	}
	// The composition rules are gone, not ignored.
	if rec := e.patch(t, ft, token, map[string]any{"security": map[string]any{"password_policy": map[string]any{"require_digit": true}}}); rec.Code != http.StatusBadRequest {
		t.Errorf("removed password rule status = %d, want 400", rec.Code)
	}
	rec := e.patch(t, ft, token, map[string]any{"security": map[string]any{"password_policy": map[string]any{"changed_at": "2020-01-01T00:00:00Z"}}})
	if rec.Code != http.StatusBadRequest || decode[errorBody](t, rec).Error.Code != "read_only_key" {
		t.Errorf("changed_at status = %d, body = %s, want 400 read_only_key", rec.Code, rec.Body.String())
	}
}

func TestPatch_MFAModeAppliesOnTheNextRequest(t *testing.T) {
	e := newEnv(t)
	ft := e.newTenant(t)
	_, adminToken := e.member(t, ft, "admin")
	_, memberToken := e.member(t, ft, "user")

	decision := func() enforce.Decision {
		t.Helper()
		authCtx, err := e.checker.Authenticate(t.Context(), memberToken, ft.id, ft.slug, clientIP, nil, nil)
		if err != nil || !authCtx.IsAuthenticated {
			t.Fatalf("Authenticate() = %+v, %v", authCtx, err)
		}
		d, err := e.checker.EnforceMFA(t.Context(), "/_m/sample/things", ft.id, authCtx)
		if err != nil {
			t.Fatalf("EnforceMFA() error: %v", err)
		}
		return d
	}

	if d := decision(); d != enforce.Allowed {
		t.Fatalf("decision under the default optional mode = %q, want allowed", d)
	}
	if rec := e.patch(t, ft, adminToken, map[string]any{"security": map[string]any{"mfa": map[string]any{"mode": "required"}}}); rec.Code != http.StatusOK {
		t.Fatalf("PATCH status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if d := decision(); d != enforce.SetupRequired {
		t.Errorf("decision after switching to required = %q, want %q", d, enforce.SetupRequired)
	}
	if rec := e.patch(t, ft, adminToken, map[string]any{"security": map[string]any{"mfa": map[string]any{"mode": "optional"}}}); rec.Code != http.StatusOK {
		t.Fatalf("PATCH status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if d := decision(); d != enforce.Allowed {
		t.Errorf("decision after switching back to optional = %q, want allowed", d)
	}
}

func TestPatch_LeavingRequiredForRolesDropsItsRoles(t *testing.T) {
	e := newEnv(t)
	ft := e.newTenant(t)
	_, token := e.member(t, ft, "admin")

	if rec := e.patch(t, ft, token, map[string]any{"security": map[string]any{"mfa": map[string]any{"mode": "required_for_roles", "required_roles": []string{"admin"}}}}); rec.Code != http.StatusOK {
		t.Fatalf("first PATCH status = %d, body = %s", rec.Code, rec.Body.String())
	}
	rec := e.patch(t, ft, token, map[string]any{"security": map[string]any{"mfa": map[string]any{"mode": "required"}}})
	if rec.Code != http.StatusOK {
		t.Fatalf("PATCH of the mode alone status = %d, body = %s, want 200", rec.Code, rec.Body.String())
	}
	if got := decode[Settings](t, rec).Security.MFA; got.Mode != "required" || len(got.RequiredRoles) != 0 {
		t.Errorf("mfa = %+v, want required with no roles", got)
	}
}

func TestPatch_ResentAllowlistIsAcceptedFromOutsideIt(t *testing.T) {
	e := newEnv(t)
	ft := e.newTenant(t)
	_, token := e.member(t, ft, "admin")
	if err := tenantconfig.NewStore(e.conn).Set(t.Context(), ft.id, ipallowlist.Key, "198.51.100.0/24"); err != nil {
		t.Fatalf("store allowlist: %v", err)
	}

	rec := e.patch(t, ft, token, map[string]any{"security": map[string]any{
		"ip_allowlist":    "198.51.100.0/24",
		"password_policy": map[string]any{"min_length": 14},
	}})
	if rec.Code != http.StatusOK {
		t.Fatalf("PATCH status = %d, body = %s, want 200 for an unchanged allowlist", rec.Code, rec.Body.String())
	}
	if got := decode[Settings](t, rec).Security.PasswordPolicy.MinLength; got != 14 {
		t.Errorf("min_length = %d, want 14", got)
	}
}

func TestPatch_StoredRoleDeletedSinceDoesNotBlockMFAEdits(t *testing.T) {
	e := newEnv(t)
	ft := e.newTenant(t)
	_, token := e.member(t, ft, "admin")
	if err := tenantconfig.NewStore(e.conn).SetMany(t.Context(), ft.id, map[string]string{
		"mfa.enforcement_mode": "required_for_roles",
		"mfa.required_roles":   "admin,deleted_role",
	}); err != nil {
		t.Fatalf("store mfa policy: %v", err)
	}

	rec := e.patch(t, ft, token, map[string]any{"security": map[string]any{"mfa": map[string]any{
		"required_roles": []string{"admin", "deleted_role"}, "max_assurance_age_hours": 48,
	}}})
	if rec.Code != http.StatusOK {
		t.Fatalf("PATCH status = %d, body = %s, want 200", rec.Code, rec.Body.String())
	}
	rec = e.patch(t, ft, token, map[string]any{"security": map[string]any{"mfa": map[string]any{
		"required_roles": []string{"admin", "never_existed"},
	}}})
	if rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("PATCH adding an unknown role status = %d, want 422", rec.Code)
	}
}

func TestPatch_IPAllowlistClears(t *testing.T) {
	e := newEnv(t)
	ft := e.newTenant(t)
	_, token := e.member(t, ft, "admin")

	for _, value := range []string{clientIP, ""} {
		rec := e.patch(t, ft, token, map[string]any{"security": map[string]any{"ip_allowlist": value}})
		if rec.Code != http.StatusOK {
			t.Fatalf("PATCH %q status = %d, body = %s", value, rec.Code, rec.Body.String())
		}
	}
	if got := e.get(t, ft, token).Security.IPAllowlist; got != "" {
		t.Errorf("ip_allowlist = %q, want cleared", got)
	}
}

func TestPatch_NameChangeReachesTenantResolution(t *testing.T) {
	e := newEnv(t)
	ft := e.newTenant(t)
	_, token := e.member(t, ft, "admin")

	// Warm the domain cache with the old name first.
	e.get(t, ft, token)
	if rec := e.patch(t, ft, token, map[string]any{"general": map[string]any{"name": "Renamed Co"}}); rec.Code != http.StatusOK {
		t.Fatalf("PATCH status = %d, body = %s", rec.Code, rec.Body.String())
	}
	tc, err := e.handler.deps.Tenants.ResolveByHost(t.Context(), ft.domain)
	if err != nil || tc.Name != "Renamed Co" {
		t.Errorf("ResolveByHost() = %+v, %v, want the new name", tc, err)
	}
}

func pngBytes(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 4, 4))
	img.Set(1, 1, color.RGBA{R: 255, A: 255})
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode png: %v", err)
	}
	return buf.Bytes()
}

func (e *env) uploadLogo(t *testing.T, ft fixtureTenant, token, filename string, content []byte) *httptest.ResponseRecorder {
	t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	part, err := mw.CreateFormFile("file", filename)
	if err != nil {
		t.Fatalf("CreateFormFile() error: %v", err)
	}
	_, _ = part.Write(content)
	_ = mw.Close()

	req := httptest.NewRequest(http.MethodPost, "/admin/settings/logo", &body)
	req.Host = ft.domain
	req.RemoteAddr = clientIP + ":44321"
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	rec := httptest.NewRecorder()
	e.handler.ServeUploadLogo(rec, req)
	return rec
}

func TestLogo_UploadThenRemove(t *testing.T) {
	e := newEnv(t)
	ft := e.newTenant(t)
	admin, token := e.member(t, ft, "admin")

	rec := e.uploadLogo(t, ft, token, "logo.png", pngBytes(t))
	if rec.Code != http.StatusOK {
		t.Fatalf("upload status = %d, body = %s", rec.Code, rec.Body.String())
	}
	uploaded := decode[logoResponse](t, rec)
	if uploaded.LogoURL == nil || !strings.HasSuffix(*uploaded.LogoURL, ".png") {
		t.Fatalf("logo_url = %v, want a .png URL", uploaded.LogoURL)
	}
	if got := e.get(t, ft, token).General.LogoURL; got == nil || *got != *uploaded.LogoURL {
		t.Errorf("GET logo_url = %v, want %q", got, *uploaded.LogoURL)
	}

	var isPublic bool
	var purpose string
	var firstKey string
	if err := e.conn.QueryRow(fmt.Sprintf(`SELECT is_public, purpose, storage_key FROM %s.files`, tenantschema.Name(ft.slug))).Scan(&isPublic, &purpose, &firstKey); err != nil {
		t.Fatalf("read files row: %v", err)
	}
	if !isPublic || purpose != logoPurpose {
		t.Errorf("files row is_public = %v, purpose = %q, want a public %q file", isPublic, purpose, logoPurpose)
	}

	// Replacing the logo deletes the old public object.
	if rec := e.uploadLogo(t, ft, token, "logo.png", pngBytes(t)); rec.Code != http.StatusOK {
		t.Fatalf("replacing upload status = %d, body = %s", rec.Code, rec.Body.String())
	}
	e.assertRetired(t, ft, firstKey)
	var secondKey string
	if err := e.conn.QueryRow(fmt.Sprintf(`SELECT storage_key FROM %s.files WHERE deleted_at IS NULL`, tenantschema.Name(ft.slug))).Scan(&secondKey); err != nil {
		t.Fatalf("read the live logo row: %v", err)
	}

	rec = e.do(t, ft, token, e.handler.ServeDeleteLogo, http.MethodDelete, "/admin/settings/logo", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("delete status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if got := e.get(t, ft, token).General.LogoURL; got != nil {
		t.Errorf("GET logo_url after delete = %q, want nil", *got)
	}
	e.assertRetired(t, ft, secondKey)
	audittest.AssertLatest(t, e.conn, ft.id, "tenant.settings_updated", "", admin)
}

func (e *env) assertRetired(t *testing.T, ft fixtureTenant, key string) {
	t.Helper()
	if exists, err := e.handler.deps.Storage.Exists(t.Context(), key); err != nil || exists {
		t.Errorf("object %s exists = %v, %v, want deleted", key, exists, err)
	}
	var deleted bool
	if err := e.conn.QueryRow(fmt.Sprintf(`SELECT deleted_at IS NOT NULL FROM %s.files WHERE storage_key = $1`, tenantschema.Name(ft.slug)), key).Scan(&deleted); err != nil || !deleted {
		t.Errorf("files row for %s deleted = %v, %v, want marked deleted", key, deleted, err)
	}
}

func TestLogo_RejectsANonImage(t *testing.T) {
	e := newEnv(t)
	ft := e.newTenant(t)
	_, token := e.member(t, ft, "admin")

	for name, content := range map[string][]byte{
		"logo.png": []byte("<html><script>alert(1)</script></html>"),
		"logo.svg": []byte(`<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`),
	} {
		if rec := e.uploadLogo(t, ft, token, name, content); rec.Code != http.StatusUnsupportedMediaType {
			t.Errorf("upload %s status = %d, want 415", name, rec.Code)
		}
	}
	if got := e.get(t, ft, token).General.LogoURL; got != nil {
		t.Errorf("logo_url = %q, want none set", *got)
	}
}

func TestLogo_RejectsAnOversizedFile(t *testing.T) {
	e := newEnv(t)
	ft := e.newTenant(t)
	_, token := e.member(t, ft, "admin")

	big := append(pngBytes(t), make([]byte, MaxLogoBytes)...)
	if rec := e.uploadLogo(t, ft, token, "logo.png", big); rec.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("status = %d, body = %s, want 413", rec.Code, rec.Body.String())
	}
}

func TestApply_ReportsFieldsCommittedBeforeAFailure(t *testing.T) {
	e := newEnv(t)
	ft := e.newTenant(t)

	committed, err := e.handler.apply(t.Context(), &tenantresolve.TenantContext{TenantID: ft.id}, &patchPlan{
		profile: &tenant.ProfileUpdate{Name: new("Committed Co")},
		// changed_at is read-only, so this write fails after the profile
		// has committed.
		l10n:    map[string]string{tenantconfig.PasswordPolicyChangedAtKey: "2020-01-01T00:00:00Z"},
		changed: []string{"general.name", "localisation.number_format"},
	})
	if err == nil {
		t.Fatal("apply() error = nil, want the failed localisation write")
	}
	if !slices.Equal(committed, []string{"general.name"}) {
		t.Errorf("committed = %v, want [general.name]", committed)
	}
}

func TestPatch_EmailVerificationFollowsThePlatformPolicy(t *testing.T) {
	e := newEnv(t)
	ft := e.newTenant(t)
	admin, token := e.member(t, ft, "admin")
	off := map[string]any{"security": map[string]any{"email_verification": map[string]any{"required": false}}}

	if rec := e.patch(t, ft, token, off); rec.Code != http.StatusOK {
		t.Fatalf("PATCH under tenant_choice = %d %s", rec.Code, rec.Body.String())
	}
	if got := e.get(t, ft, token).Security.EmailVerification; got != (EmailVerification{Policy: VerificationTenantChoice, Required: false}) {
		t.Errorf("email_verification = %+v, want tenant_choice, not required", got)
	}
	audittest.AssertLatest(t, e.conn, ft.id, "tenant.settings_updated", "", admin)

	for _, policy := range []string{VerificationRequired, VerificationOff} {
		e.handler.deps.EmailVerificationPolicy = policy
		want := EmailVerification{Policy: policy, Required: policy == VerificationRequired}
		if got := e.get(t, ft, token).Security.EmailVerification; got != want {
			t.Errorf("%s: email_verification = %+v, want %+v", policy, got, want)
		}
		flip := map[string]any{"security": map[string]any{"email_verification": map[string]any{"required": !want.Required}}}
		rec := e.patch(t, ft, token, flip)
		if got := decode[errorBody](t, rec); rec.Code != http.StatusUnprocessableEntity || got.Error.Details["field"] != "security.email_verification" {
			t.Errorf("%s: PATCH changing it = %d %s, want 422 on security.email_verification", policy, rec.Code, rec.Body.String())
		}
		same := map[string]any{"security": map[string]any{"email_verification": map[string]any{"required": want.Required}}}
		if rec := e.patch(t, ft, token, same); rec.Code != http.StatusOK {
			t.Errorf("%s: PATCH resending it = %d %s, want 200", policy, rec.Code, rec.Body.String())
		}
	}
}
