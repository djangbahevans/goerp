package adminconnectors

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json/v2"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
	"uuid"

	"github.com/djangbahevans/goerp/internal/engine/abi"
	"github.com/djangbahevans/goerp/internal/engine/apikey"
	"github.com/djangbahevans/goerp/internal/engine/auth/authcheck"
	"github.com/djangbahevans/goerp/internal/engine/auth/authtest"
	"github.com/djangbahevans/goerp/internal/engine/auth/authtoken"
	"github.com/djangbahevans/goerp/internal/engine/auth/rowcrypt"
	"github.com/djangbahevans/goerp/internal/engine/auth/session"
	"github.com/djangbahevans/goerp/internal/engine/auth/sessionrevoke"
	"github.com/djangbahevans/goerp/internal/engine/billing"
	"github.com/djangbahevans/goerp/internal/engine/cache"
	"github.com/djangbahevans/goerp/internal/engine/connectoringress"
	"github.com/djangbahevans/goerp/internal/engine/db"
	"github.com/djangbahevans/goerp/internal/engine/manifest"
	"github.com/djangbahevans/goerp/internal/engine/module"
	"github.com/djangbahevans/goerp/internal/engine/permcache"
	"github.com/djangbahevans/goerp/internal/engine/permission"
	"github.com/djangbahevans/goerp/internal/engine/providerselect"
	"github.com/djangbahevans/goerp/internal/engine/registry"
	"github.com/djangbahevans/goerp/internal/engine/role"
	"github.com/djangbahevans/goerp/internal/engine/route"
	"github.com/djangbahevans/goerp/internal/engine/tenant"
	tenantresolve "github.com/djangbahevans/goerp/internal/engine/tenant/resolve"
	"github.com/djangbahevans/goerp/internal/engine/tenantconfig"
	"github.com/djangbahevans/goerp/internal/engine/tenantschema"
	"github.com/djangbahevans/goerp/internal/engine/user"
)

const localPostgresDSN = "postgres://goerp:dev@localhost:15432/goerp"

type spyAudit struct {
	mu     sync.Mutex
	events []map[string]any
}

func (a *spyAudit) Emit(_ context.Context, tenantSlug, eventName, userID, actorUserID string, payload map[string]any) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.events = append(a.events, map[string]any{"tenant": tenantSlug, "event": eventName, "actor_user_id": actorUserID, "payload": payload})
	return nil
}

func (a *spyAudit) named(event string) []map[string]any {
	return slices.DeleteFunc(a.all(), func(e map[string]any) bool { return e["event"] != event })
}

func (a *spyAudit) all() []map[string]any {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]map[string]any(nil), a.events...)
}

type fixture struct {
	handler    *Handler
	issuer     *authtoken.Issuer
	audit      *spyAudit
	resolver   *tenantconfig.Resolver
	selection  *providerselect.Store
	endpoints  *connectoringress.Store
	domain     string
	tenantID   string
	tenantSlug string
	conn       *sql.DB
	roleStore  *role.Store
}

func connectorManifest(name, display string, provides map[string]bool, schema []manifest.ConfigEntry) manifest.Manifest {
	return manifest.Manifest{Name: name, DisplayName: display, Type: "connector", Version: "1.2.0", Description: display + " connector", Provides: provides, ConfigSchema: schema}
}

var paystackSchema = []manifest.ConfigEntry{
	{Key: "secret_key", Label: "Secret Key", Type: "string", Required: true, Encrypted: true, Category: "API Credentials"},
	{Key: "webhook_secret", Label: "Webhook Secret", Type: "string", Encrypted: true, Generated: true},
	{Key: "signing_note", Label: "Signing note", Type: "string", Generated: true},
	{Key: "test_mode", Label: "Test Mode", Type: "boolean", Default: false},
	{Key: "currency", Label: "Currency", Type: "string", Default: "GHS", FieldType: "select", Options: []manifest.FieldOption{{Value: "GHS", Label: "Cedi"}, {Value: "NGN", Label: "Naira"}}},
	{Key: "retries", Label: "Retries", Type: "integer", Default: float64(3), Min: float64(0), Max: float64(5)},
	{Key: "volume_cap", Label: "Volume cap", Type: "integer", Encrypted: true},
	{Key: "tags", Label: "Tags", Type: "string[]", Default: []any{}, Options: []manifest.FieldOption{{Value: "a", Label: "A"}, {Value: "b", Label: "B"}}},
	{Key: "account_ref", Label: "Account ref", Type: "string", ValidationRegex: `^[A-Z]{3}-\d+$`, Default: nil},
}

func newFixture(t *testing.T) *fixture {
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
	roleStore := role.NewStore(conn)
	apiKeys := apikey.NewStore(conn)
	billingStore := billing.NewStore(conn)
	selection := providerselect.NewStore(conn)
	configStore := tenantconfig.NewStore(conn)
	endpoints := connectoringress.NewStore(conn)
	for name, bootstrap := range map[string]func(context.Context) error{
		"tenant": tenantStore.Bootstrap, "user": userStore.Bootstrap, "session": sessionStore.Bootstrap,
		"apikey": apiKeys.Bootstrap, "billing": billingStore.Bootstrap, "providerselect": selection.Bootstrap,
		"tenantconfig": configStore.Bootstrap, "connectoringress": endpoints.Bootstrap,
	} {
		if err := bootstrap(ctx); err != nil {
			t.Fatalf("%s Bootstrap() error: %v", name, err)
		}
	}

	slug := fmt.Sprintf("adminconntest%d", time.Now().UnixNano())
	tt, err := tenantStore.CreateTenant(ctx, slug, "Admin Connectors Test Co")
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
	if _, err := conn.Exec(fmt.Sprintf(`CREATE TABLE %s.module_config (
		module_name TEXT NOT NULL, key TEXT NOT NULL, value JSONB NOT NULL, value_type TEXT NOT NULL,
		encrypted BOOLEAN NOT NULL DEFAULT FALSE, updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(), updated_by UUID,
		PRIMARY KEY (module_name, key))`, schema)); err != nil {
		t.Fatalf("create module_config: %v", err)
	}
	if err := roleStore.Bootstrap(ctx, slug); err != nil {
		t.Fatalf("role Bootstrap() error: %v", err)
	}
	if err := roleStore.SeedBuiltinRoles(ctx, slug); err != nil {
		t.Fatalf("SeedBuiltinRoles() error: %v", err)
	}
	roleMap := permcache.NewRolePermissionMap()
	if err := roleMap.RebuildAll(ctx, tenantStore, roleStore, permission.NewPermissionRegistry()); err != nil {
		t.Fatalf("RebuildAll() error: %v", err)
	}

	reg := &registry.ModuleRegistry{}
	if _, err := reg.Update(map[string]*module.LoadedModule{
		"connector_paystack":       {Status: module.StatusReady, HasWebhookVerifier: true, Capabilities: abi.CapDBRead, Manifest: connectorManifest("connector_paystack", "Paystack", map[string]bool{"payment_provider": true}, paystackSchema)},
		"connector_twilio":         {Status: module.StatusReady, Capabilities: abi.CapDBRead, Manifest: connectorManifest("connector_twilio", "Twilio", map[string]bool{"sms_provider": true}, []manifest.ConfigEntry{{Key: "sender_id", Label: "Sender", Type: "string"}})},
		"connector_africastalking": {Status: module.StatusReady, Capabilities: abi.CapDBRead, Manifest: connectorManifest("connector_africastalking", "Africa's Talking", map[string]bool{"sms_provider": true}, nil)},
		"connector_broken":         {Status: module.StatusFailed, Manifest: connectorManifest("connector_broken", "Broken", nil, nil)},
		"sales":                    {Status: module.StatusReady, Capabilities: abi.CapDBRead, Manifest: manifest.Manifest{Name: "sales", Type: "domain", ConfigSchema: []manifest.ConfigEntry{{Key: "prefix", Label: "Prefix", Type: "string", Default: "INV"}}}},
	}); err != nil {
		t.Fatalf("registry Update: %v", err)
	}

	resolver := tenantconfig.NewResolver(configStore, tenantStore, reg)
	audit := &spyAudit{}
	signingKeySet := authtest.SigningKeys()
	authChecker := authcheck.NewChecker(&signingKeySet.Active, sessionrevoke.NewRevoker(sessionStore, cacheClient), userStore, roleStore,
		permcache.NewRoleCache(cacheClient), roleMap, apiKeys, false, nil, nil, nil)

	handler := NewHandler(Deps{
		Tenants:   tenantresolve.NewResolver(tenantStore, cacheClient, billingStore),
		Auth:      authChecker,
		Registry:  reg,
		Config:    configStore,
		Cache:     resolver,
		Providers: selection,
		Endpoints: endpoints,
		Modules:   billingStore,
		Keys:      &rowcrypt.RowKeySet{Active: rowcrypt.RowKey{KeyID: "test-key", Key: make([]byte, 32)}},
		Audit:     audit,
	})
	return &fixture{
		handler: handler, issuer: authtoken.NewIssuer(&signingKeySet.Active, tenantStore, roleStore, sessionStore),
		audit: audit, resolver: resolver, selection: selection, endpoints: endpoints, domain: domain, tenantID: tt.ID, tenantSlug: slug, conn: conn, roleStore: roleStore,
	}
}

func (f *fixture) tokenFor(t *testing.T, roleName string) string {
	t.Helper()
	ctx := t.Context()
	email := fmt.Sprintf("adminconntest%d@example.com", time.Now().UnixNano())
	userID, err := user.NewStore(f.conn).FindOrCreateInvited(ctx, email)
	if err != nil {
		t.Fatalf("FindOrCreateInvited() error: %v", err)
	}
	t.Cleanup(func() { _, _ = f.conn.Exec(`DELETE FROM system.users WHERE id = $1`, userID) })
	t.Cleanup(func() { _, _ = f.conn.Exec(`DELETE FROM system.sessions WHERE user_id = $1`, userID) })
	if _, err := f.conn.Exec(`UPDATE system.users SET status = 'active' WHERE id = $1`, userID); err != nil {
		t.Fatalf("activate user: %v", err)
	}
	roleID, err := f.roleStore.GetRoleByName(ctx, f.tenantSlug, roleName)
	if err != nil {
		t.Fatalf("GetRoleByName(%q) error: %v", roleName, err)
	}
	schema := tenantschema.Name(f.tenantSlug)
	if _, err := f.conn.Exec(fmt.Sprintf("WITH m AS (INSERT INTO %[1]s.tenant_members (user_id) VALUES ($1) ON CONFLICT DO NOTHING) INSERT INTO %[1]s.user_roles (user_id, role_id) VALUES ($1, $2)", schema), userID, roleID); err != nil {
		t.Fatalf("grant role: %v", err)
	}
	tokens, err := f.issuer.Issue(ctx, authtoken.LoginParams{UserID: userID, TenantSlug: f.tenantSlug, DeviceID: uuid.New().String()})
	if err != nil {
		t.Fatalf("Issue() error: %v", err)
	}
	return tokens.AccessToken
}

func (f *fixture) do(h http.HandlerFunc, method, path, token string, params map[string]string, body any) *httptest.ResponseRecorder {
	var reader *bytes.Reader
	if body != nil {
		data, _ := json.Marshal(body)
		reader = bytes.NewReader(data)
	} else {
		reader = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, path, reader)
	req = req.WithContext(route.WithParams(req.Context(), params))
	req.Host = f.domain
	req.RemoteAddr = "203.0.113.7:54321"
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	h(rec, req)
	return rec
}

func (f *fixture) list(token string) *httptest.ResponseRecorder {
	return f.do(f.handler.ServeList, http.MethodGet, "/admin/connectors", token, nil, nil)
}

func (f *fixture) get(token, name string) *httptest.ResponseRecorder {
	return f.do(f.handler.ServeGet, http.MethodGet, "/admin/connectors/"+name, token, map[string]string{"name": name}, nil)
}

func (f *fixture) patch(token string, body any) *httptest.ResponseRecorder {
	return f.do(f.handler.ServePatchConfig, http.MethodPatch, "/admin/config", token, nil, body)
}

func (f *fixture) rotate(token, name, key string) *httptest.ResponseRecorder {
	return f.do(f.handler.ServeRotate, http.MethodPost, "/admin/connectors/"+name+"/config/"+key+"/rotate", token, map[string]string{"name": name, "key": key}, nil)
}

func (f *fixture) revokeWebhook(token, name string) *httptest.ResponseRecorder {
	return f.do(f.handler.ServeRevokeWebhook, http.MethodDelete, "/admin/connectors/"+name+"/webhook", token, map[string]string{"name": name}, nil)
}

func decode[T any](t *testing.T, rec *httptest.ResponseRecorder) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
		t.Fatalf("decode %q: %v", rec.Body.String(), err)
	}
	return v
}

type errorBody struct {
	Error struct {
		Code    string            `json:"code"`
		Details map[string]string `json:"details"`
	} `json:"error"`
}

func (f *fixture) storedRow(t *testing.T, module, key string) (value string, encrypted, found bool) {
	t.Helper()
	err := f.conn.QueryRow(fmt.Sprintf(`SELECT value::text, encrypted FROM %s.module_config WHERE module_name = $1 AND key = $2`, tenantschema.Name(f.tenantSlug)), module, key).Scan(&value, &encrypted)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, false
	}
	if err != nil {
		t.Fatalf("read stored row: %v", err)
	}
	return value, encrypted, true
}

func TestEndpointsRequireAnAdmin(t *testing.T) {
	f := newFixture(t)
	member := f.tokenFor(t, "user")

	for name, rec := range map[string]*httptest.ResponseRecorder{
		"list anonymous":   f.list(""),
		"get anonymous":    f.get("", "connector_paystack"),
		"patch anonymous":  f.patch("", map[string]any{"connector_paystack.test_mode": true}),
		"rotate anonymous": f.rotate("", "connector_paystack", "signing_note"),
	} {
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("%s: status = %d, want 401", name, rec.Code)
		}
	}
	for name, rec := range map[string]*httptest.ResponseRecorder{
		"list member":   f.list(member),
		"get member":    f.get(member, "connector_paystack"),
		"patch member":  f.patch(member, map[string]any{"connector_paystack.test_mode": true}),
		"rotate member": f.rotate(member, "connector_paystack", "signing_note"),
	} {
		if rec.Code != http.StatusForbidden {
			t.Errorf("%s: status = %d, want 403", name, rec.Code)
		}
	}
}

func TestListShowsConnectorsWithConfiguredAndProviderState(t *testing.T) {
	f := newFixture(t)
	admin := f.tokenFor(t, "admin")
	for module, category := range map[string]string{"connector_twilio": "sms_provider", "connector_africastalking": "sms_provider", "connector_paystack": "payment_provider"} {
		if _, err := f.conn.Exec(`INSERT INTO system.tenant_module_settings (tenant_id, module_name, enabled, provider_category) VALUES ($1, $2, true, $3)`, f.tenantID, module, category); err != nil {
			t.Fatalf("install %s: %v", module, err)
		}
	}

	type listBody struct {
		Connectors []struct {
			Name       string `json:"name"`
			Display    string `json:"display_name"`
			Version    string `json:"version"`
			Enabled    bool   `json:"enabled"`
			Configured bool   `json:"configured"`
			Status     bool   `json:"has_status_route"`
			Provider   *struct {
				Category      string `json:"category"`
				Primary       bool   `json:"primary"`
				CanSetPrimary bool   `json:"can_set_primary"`
			} `json:"provider"`
		} `json:"connectors"`
	}
	rec := f.list(admin)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body)
	}
	got := decode[listBody](t, rec)

	var names []string
	for _, c := range got.Connectors {
		names = append(names, c.Name)
	}
	if strings.Join(names, ",") != "connector_africastalking,connector_paystack,connector_twilio" {
		t.Fatalf("connectors = %v, want the three healthy connectors sorted by display name and no domain or failed module", names)
	}
	by := map[string]int{}
	for i, c := range got.Connectors {
		by[c.Name] = i
	}
	paystack, twilio := got.Connectors[by["connector_paystack"]], got.Connectors[by["connector_twilio"]]
	if paystack.Configured {
		t.Error("paystack is configured with its required secret_key unset")
	}
	if !twilio.Configured || twilio.Version != "1.2.0" || !twilio.Enabled || twilio.Status {
		t.Errorf("twilio = %+v, want configured, enabled, version 1.2.0 and no status route", twilio)
	}
	if paystack.Provider != nil {
		t.Errorf("paystack provider = %+v, want none: payment_provider is multi-active", paystack.Provider)
	}
	if twilio.Provider == nil || twilio.Provider.Category != "sms_provider" || twilio.Provider.Primary || !twilio.Provider.CanSetPrimary {
		t.Errorf("twilio provider = %+v, want an sms provider that can be set primary but is not yet", twilio.Provider)
	}

	if _, err := f.selection.SetPrimary(t.Context(), f.tenantID, "connector_twilio", ""); err != nil {
		t.Fatalf("SetPrimary: %v", err)
	}
	got = decode[listBody](t, f.list(admin))
	for _, c := range got.Connectors {
		switch c.Name {
		case "connector_twilio":
			if c.Provider == nil || !c.Provider.Primary {
				t.Errorf("twilio provider = %+v after SetPrimary, want primary", c.Provider)
			}
		case "connector_africastalking":
			if c.Provider == nil || c.Provider.Primary || !c.Provider.CanSetPrimary {
				t.Errorf("africastalking provider = %+v, want a non-primary sms provider that can be set", c.Provider)
			}
		}
	}

	if _, err := f.conn.Exec(`UPDATE system.tenant_module_settings SET enabled = false WHERE tenant_id = $1 AND module_name = 'connector_africastalking'`, f.tenantID); err != nil {
		t.Fatalf("disable module: %v", err)
	}
	for _, c := range decode[listBody](t, f.list(admin)).Connectors {
		if c.Name == "connector_africastalking" && c.Enabled {
			t.Error("a module the tenant disabled is listed as enabled")
		}
		if c.Name == "connector_twilio" && c.Provider != nil && c.Provider.CanSetPrimary {
			t.Error("twilio can still be set primary with the only other provider disabled")
		}
	}
}

func TestDetailMasksEncryptedValuesAndShowsTheRest(t *testing.T) {
	f := newFixture(t)
	admin := f.tokenFor(t, "admin")
	if rec := f.patch(admin, map[string]any{"connector_paystack.secret_key": "sk_live_topsecret", "connector_paystack.currency": "NGN", "connector_paystack.retries": 4}); rec.Code != http.StatusOK {
		t.Fatalf("patch status = %d: %s", rec.Code, rec.Body)
	}

	rec := f.get(admin, "connector_paystack")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body)
	}
	if strings.Contains(rec.Body.String(), "sk_live_topsecret") {
		t.Fatal("the response contains the plaintext secret")
	}
	type entry struct {
		Key       string `json:"key"`
		Encrypted bool   `json:"encrypted"`
		Generated bool   `json:"generated"`
		IsSet     bool   `json:"is_set"`
		Value     any    `json:"value"`
		Default   any    `json:"default"`
	}
	body := decode[struct {
		Name       string  `json:"name"`
		Configured bool    `json:"configured"`
		Config     []entry `json:"config"`
	}](t, rec)
	if !body.Configured {
		t.Error("detail not configured after the required secret was set")
	}
	by := map[string]entry{}
	for _, e := range body.Config {
		by[e.Key] = e
	}
	if e := by["secret_key"]; e.Value != "***" || !e.IsSet || !e.Encrypted {
		t.Errorf("secret_key = %+v, want a set encrypted entry whose value is ***", e)
	}
	if e := by["webhook_secret"]; e.Value != nil || e.IsSet || !e.Generated {
		t.Errorf("webhook_secret = %+v, want an unset generated entry with a null value", e)
	}
	if e := by["currency"]; e.Value != "NGN" || e.Default != "GHS" {
		t.Errorf("currency = %+v, want the stored NGN over the GHS default", e)
	}
	if e := by["retries"]; e.Value != float64(4) {
		t.Errorf("retries = %+v, want 4", e)
	}
	if e := by["test_mode"]; e.Value != false || e.IsSet {
		t.Errorf("test_mode = %+v, want the false default, unset", e)
	}

	if rec := f.get(admin, "sales"); rec.Code != http.StatusNotFound {
		t.Errorf("a domain module's detail: status = %d, want 404", rec.Code)
	}
	if rec := f.get(admin, "connector_broken"); rec.Code != http.StatusNotFound {
		t.Errorf("a failed connector's detail: status = %d, want 404", rec.Code)
	}
	if rec := f.get(admin, "connector_missing"); rec.Code != http.StatusNotFound {
		t.Errorf("an unknown connector's detail: status = %d, want 404", rec.Code)
	}
}

func TestPatchEncryptsAtRestAuditsAndInvalidatesTheCache(t *testing.T) {
	f := newFixture(t)
	admin := f.tokenFor(t, "admin")
	ctx := t.Context()

	if _, _, found, err := f.resolver.Get(ctx, f.tenantID, "connector_paystack.currency"); err != nil || found {
		t.Fatalf("priming resolver: found=%v err=%v", found, err)
	}

	rec := f.patch(admin, map[string]any{
		"connector_paystack.secret_key": "sk_live_topsecret",
		"connector_paystack.test_mode":  true,
		"connector_paystack.tags":       []any{"a", "b"},
		"connector_paystack.currency":   "NGN",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body)
	}
	resp := decode[struct {
		ModuleName string   `json:"module_name"`
		Updated    []string `json:"updated"`
		Configured bool     `json:"configured"`
	}](t, rec)
	if resp.ModuleName != "connector_paystack" || len(resp.Updated) != 4 || !resp.Configured {
		t.Errorf("response = %+v, want four updated keys and configured", resp)
	}

	value, encrypted, found := f.storedRow(t, "connector_paystack", "secret_key")
	if !found || !encrypted || strings.Contains(value, "sk_live_topsecret") {
		t.Errorf("stored secret_key = %q encrypted=%v found=%v, want ciphertext flagged encrypted", value, encrypted, found)
	}
	if v, enc, _ := f.storedRow(t, "connector_paystack", "currency"); enc || v != `"NGN"` {
		t.Errorf("stored currency = %s encrypted=%v, want plain \"NGN\"", v, enc)
	}

	if v, _, found, err := f.resolver.Get(ctx, f.tenantID, "connector_paystack.currency"); err != nil || !found || v != "NGN" {
		t.Errorf("resolver after the write = %q found=%v err=%v, want NGN: the cache entry must be invalidated", v, found, err)
	}

	events := f.audit.named("module.config_changed")
	if len(events) != 1 {
		t.Fatalf("audit events = %v, want one module.config_changed", events)
	}
	if fmt.Sprint(events[0]) == "" || strings.Contains(fmt.Sprint(events[0]), "sk_live_topsecret") {
		t.Errorf("audit event %v leaks the secret", events[0])
	}
}

func TestPatchRejectsInvalidValuesAtomically(t *testing.T) {
	f := newFixture(t)
	admin := f.tokenFor(t, "admin")

	tests := []struct {
		name string
		body map[string]any
		key  string
	}{
		{"unknown key", map[string]any{"connector_paystack.nope": 1}, "connector_paystack.nope"},
		{"unknown module", map[string]any{"ghost.key": 1}, "ghost.key"},
		{"generated key", map[string]any{"connector_paystack.webhook_secret": "x"}, "connector_paystack.webhook_secret"},
		{"wrong type", map[string]any{"connector_paystack.test_mode": "yes"}, "connector_paystack.test_mode"},
		{"fractional integer", map[string]any{"connector_paystack.retries": 2.5}, "connector_paystack.retries"},
		{"above the maximum", map[string]any{"connector_paystack.retries": 9}, "connector_paystack.retries"},
		{"below the minimum", map[string]any{"connector_paystack.retries": -1}, "connector_paystack.retries"},
		{"not an option", map[string]any{"connector_paystack.currency": "USD"}, "connector_paystack.currency"},
		{"array item not an option", map[string]any{"connector_paystack.tags": []any{"a", "z"}}, "connector_paystack.tags"},
		{"array of the wrong type", map[string]any{"connector_paystack.tags": []any{1}}, "connector_paystack.tags"},
		{"regex mismatch", map[string]any{"connector_paystack.account_ref": "abc"}, "connector_paystack.account_ref"},
		{"required cleared", map[string]any{"connector_paystack.secret_key": nil}, "secret_key"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body := map[string]any{"connector_paystack.test_mode": true}
			maps.Copy(body, tt.body)
			rec := f.patch(admin, body)
			if rec.Code != http.StatusUnprocessableEntity {
				t.Fatalf("status = %d, want 422: %s", rec.Code, rec.Body)
			}
			details := decode[errorBody](t, rec).Error.Details
			if _, ok := details[tt.key]; !ok {
				t.Errorf("details = %v, want a message for %s", details, tt.key)
			}
			if _, _, found := f.storedRow(t, "connector_paystack", "test_mode"); found {
				t.Error("a valid key was written although another key of the request was rejected")
			}
		})
	}

	if rec := f.patch(admin, map[string]any{"connector_paystack.test_mode": true, "sales.prefix": "X"}); rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("keys of two modules: status = %d, want 422", rec.Code)
	}
	for _, body := range []any{nil, map[string]any{}, []any{1}, "text"} {
		if rec := f.patch(admin, body); rec.Code != http.StatusBadRequest {
			t.Errorf("body %v: status = %d, want 400", body, rec.Code)
		}
	}
}

func TestPatchNullResetsToDefaultAndMaskedMeansUnchanged(t *testing.T) {
	f := newFixture(t)
	admin := f.tokenFor(t, "admin")
	f.patch(admin, map[string]any{"connector_paystack.secret_key": "sk_one", "connector_paystack.currency": "NGN"})
	before, _, _ := f.storedRow(t, "connector_paystack", "secret_key")

	rec := f.patch(admin, map[string]any{"connector_paystack.secret_key": "***", "connector_paystack.currency": nil})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body)
	}

	if after, _, _ := f.storedRow(t, "connector_paystack", "secret_key"); after != before {
		t.Error("the masked value overwrote the stored secret")
	}
	if _, _, found := f.storedRow(t, "connector_paystack", "currency"); found {
		t.Error("a null write left the stored value in place")
	}

	rec = f.patch(admin, map[string]any{"connector_paystack.secret_key": "***"})
	if rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), "secret_key\"") && !strings.Contains(rec.Body.String(), `"updated":[]`) {
		t.Errorf("a masked-only patch: status %d body %s, want a 200 no-op", rec.Code, rec.Body)
	}
	if got := len(f.audit.named("module.config_changed")); got != 2 {
		t.Errorf("config_changed audit events = %d, want only the two real writes", got)
	}
}

func TestRotateMintsANewGeneratedValue(t *testing.T) {
	f := newFixture(t)
	admin := f.tokenFor(t, "admin")

	rec := f.rotate(admin, "connector_paystack", "signing_note")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body)
	}
	first := decode[struct {
		Value string `json:"value"`
	}](t, rec).Value
	if len(first) != 64 {
		t.Errorf("rotated value = %q, want 64 hex characters", first)
	}
	again := decode[struct {
		Value string `json:"value"`
	}](t, f.rotate(admin, "connector_paystack", "signing_note")).Value
	if again == first {
		t.Error("two rotations produced the same value")
	}
	if stored, _, _ := f.storedRow(t, "connector_paystack", "signing_note"); stored != `"`+again+`"` {
		t.Errorf("stored = %s, want the latest rotated value", stored)
	}

	rec = f.rotate(admin, "connector_paystack", "webhook_secret")
	if rec.Code != http.StatusOK || strings.Contains(strings.ToLower(rec.Body.String()), "\"value\":\""+first) {
		t.Fatalf("encrypted rotate: status %d body %s", rec.Code, rec.Body)
	}
	if got := decode[struct {
		Value string `json:"value"`
	}](t, rec).Value; got != "***" {
		t.Errorf("an encrypted rotated value is returned as %q, want ***", got)
	}
	if v, enc, found := f.storedRow(t, "connector_paystack", "webhook_secret"); !found || !enc || len(v) < 60 {
		t.Errorf("stored webhook_secret = %q encrypted=%v found=%v, want ciphertext", v, enc, found)
	}

	for name, rec := range map[string]*httptest.ResponseRecorder{
		"not generated":     f.rotate(admin, "connector_paystack", "test_mode"),
		"unknown key":       f.rotate(admin, "connector_paystack", "nope"),
		"unknown connector": f.rotate(admin, "connector_missing", "signing_note"),
	} {
		if rec.Code == http.StatusOK {
			t.Errorf("%s: status = 200, want a rejection", name)
		}
	}
	if len(f.audit.all()) != 3 {
		t.Errorf("audit events = %d, want one per rotation", len(f.audit.all()))
	}
}

func TestPatchStoresLargeEncryptedIntegersThatRoundTrip(t *testing.T) {
	f := newFixture(t)
	admin := f.tokenFor(t, "admin")

	rec := f.patch(admin, map[string]any{"connector_paystack.volume_cap": 1000000})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body)
	}

	value, _, found, err := f.resolver.Get(t.Context(), f.tenantID, "connector_paystack.volume_cap")
	if err != nil || !found {
		t.Fatalf("resolver Get: found=%v err=%v", found, err)
	}
	plaintext, err := f.handler.Keys.Decrypt([]byte(value))
	if err != nil || string(plaintext) != "1000000" {
		t.Errorf("decrypted value = %q, %v; want 1000000, not an exponent form", plaintext, err)
	}
}

func TestPatchMissingEncryptionKeyIsAServerError(t *testing.T) {
	f := newFixture(t)
	admin := f.tokenFor(t, "admin")
	f.handler.Keys = nil

	rec := f.patch(admin, map[string]any{"connector_paystack.secret_key": "sk_live_x"})

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500 rather than a 422 blaming the input: %s", rec.Code, rec.Body)
	}
}

type webhookPathBody struct {
	WebhookPath string `json:"webhook_path"`
}

func TestPatchMintsTheWebhookEndpointOnFirstCompleteSaveOnly(t *testing.T) {
	f := newFixture(t)
	admin := f.tokenFor(t, "admin")
	ctx := t.Context()

	if rec := f.patch(admin, map[string]any{"connector_paystack.test_mode": true}); rec.Code != http.StatusOK {
		t.Fatalf("incomplete save status = %d: %s", rec.Code, rec.Body)
	} else if got := decode[webhookPathBody](t, rec); got.WebhookPath != "" {
		t.Errorf("incomplete save webhook_path = %q, want none", got.WebhookPath)
	}
	if _, err := f.endpoints.ActiveEndpoint(ctx, f.tenantID, "connector_paystack"); !errors.Is(err, connectoringress.ErrEndpointNotFound) {
		t.Fatalf("ActiveEndpoint after an incomplete save: err = %v, want ErrEndpointNotFound", err)
	}
	if got := decode[webhookPathBody](t, f.get(admin, "connector_paystack")); got.WebhookPath != "" {
		t.Errorf("detail before minting webhook_path = %q, want none", got.WebhookPath)
	}

	rec := f.patch(admin, map[string]any{"connector_paystack.secret_key": "sk_live_x"})
	if rec.Code != http.StatusOK {
		t.Fatalf("complete save status = %d: %s", rec.Code, rec.Body)
	}
	token, err := f.endpoints.ActiveEndpoint(ctx, f.tenantID, "connector_paystack")
	if err != nil {
		t.Fatalf("ActiveEndpoint after a complete save: %v", err)
	}
	want := "/_webhooks/connector_paystack/" + token
	if got := decode[webhookPathBody](t, rec); got.WebhookPath != want {
		t.Errorf("save webhook_path = %q, want %q", got.WebhookPath, want)
	}
	if got := decode[webhookPathBody](t, f.get(admin, "connector_paystack")); got.WebhookPath != want {
		t.Errorf("detail webhook_path = %q, want %q", got.WebhookPath, want)
	}

	if rec := f.patch(admin, map[string]any{"connector_paystack.currency": "NGN"}); rec.Code != http.StatusOK {
		t.Fatalf("later save status = %d: %s", rec.Code, rec.Body)
	}
	if again, err := f.endpoints.ActiveEndpoint(ctx, f.tenantID, "connector_paystack"); err != nil || again != token {
		t.Errorf("token after a later save = %q err=%v, want the unchanged %q", again, err, token)
	}

	minted := f.audit.named("module.webhook_endpoint_minted")
	if len(minted) != 1 {
		t.Fatalf("minted audit events = %d, want 1", len(minted))
	}
	if strings.Contains(fmt.Sprint(minted[0]), token) {
		t.Errorf("audit event %v leaks the token", minted[0])
	}
}

func TestPatchMintsNoEndpointForAConnectorWithoutAWebhookVerifier(t *testing.T) {
	f := newFixture(t)
	admin := f.tokenFor(t, "admin")

	if rec := f.patch(admin, map[string]any{"connector_twilio.sender_id": "GoERP"}); rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body)
	}
	if _, err := f.endpoints.ActiveEndpoint(t.Context(), f.tenantID, "connector_twilio"); !errors.Is(err, connectoringress.ErrEndpointNotFound) {
		t.Errorf("ActiveEndpoint: err = %v, want ErrEndpointNotFound", err)
	}
}

func TestRevokeWebhookStopsResolvingAndTheNextCompleteSaveMintsANewToken(t *testing.T) {
	f := newFixture(t)
	admin := f.tokenFor(t, "admin")
	ctx := t.Context()

	if rec := f.revokeWebhook(admin, "connector_paystack"); rec.Code != http.StatusNotFound {
		t.Errorf("revoke without an endpoint: status = %d, want 404", rec.Code)
	}
	if rec := f.patch(admin, map[string]any{"connector_paystack.secret_key": "sk_live_x"}); rec.Code != http.StatusOK {
		t.Fatalf("save status = %d: %s", rec.Code, rec.Body)
	}
	first, err := f.endpoints.ActiveEndpoint(ctx, f.tenantID, "connector_paystack")
	if err != nil {
		t.Fatalf("ActiveEndpoint: %v", err)
	}

	if rec := f.revokeWebhook(admin, "connector_paystack"); rec.Code != http.StatusNoContent {
		t.Fatalf("revoke status = %d: %s", rec.Code, rec.Body)
	}
	if _, err := f.endpoints.ResolveEndpoint(ctx, first, "connector_paystack"); !errors.Is(err, connectoringress.ErrEndpointNotFound) {
		t.Errorf("ResolveEndpoint of the revoked token: err = %v, want ErrEndpointNotFound", err)
	}
	if got := decode[webhookPathBody](t, f.get(admin, "connector_paystack")); got.WebhookPath != "" {
		t.Errorf("detail after revoking webhook_path = %q, want none", got.WebhookPath)
	}
	if rec := f.revokeWebhook(admin, "connector_paystack"); rec.Code != http.StatusNotFound {
		t.Errorf("second revoke: status = %d, want 404", rec.Code)
	}

	if rec := f.patch(admin, map[string]any{"connector_paystack.currency": "NGN"}); rec.Code != http.StatusOK {
		t.Fatalf("save after revoking status = %d: %s", rec.Code, rec.Body)
	}
	second, err := f.endpoints.ActiveEndpoint(ctx, f.tenantID, "connector_paystack")
	if err != nil || second == first {
		t.Errorf("token after re-saving = %q err=%v, want a new token different from %q", second, err, first)
	}
}

func TestRevokeWebhookRequiresAnAdminAndAnInstalledConnector(t *testing.T) {
	f := newFixture(t)
	if rec := f.revokeWebhook(f.tokenFor(t, "user"), "connector_paystack"); rec.Code != http.StatusForbidden {
		t.Errorf("member revoke: status = %d, want 403", rec.Code)
	}
	if rec := f.revokeWebhook(f.tokenFor(t, "admin"), "connector_missing"); rec.Code != http.StatusNotFound {
		t.Errorf("unknown connector revoke: status = %d, want 404", rec.Code)
	}
}
