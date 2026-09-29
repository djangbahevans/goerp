package notifconfig

import (
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/auth/rowcrypt"
	"github.com/djangbahevans/goerp/internal/engine/db"
	"github.com/djangbahevans/goerp/internal/engine/notifications"
	"github.com/djangbahevans/goerp/internal/engine/registry"
	"github.com/djangbahevans/goerp/internal/engine/tenant"
	"github.com/djangbahevans/goerp/internal/engine/tenantconfig"
	"github.com/djangbahevans/goerp/internal/engine/tenantschema"
)

const localPostgresDSN = "postgres://goerp:dev@localhost:15432/goerp"

func TestDefaultChannels_TenantOverrideBeatsManifestDefault(t *testing.T) {
	manifestDefault := []string{notifications.ChannelInApp, notifications.ChannelEmail}
	c := &Config{Defaults: map[string][]string{"sales.order_confirmed": {notifications.ChannelInApp}}}

	if got := c.DefaultChannels("sales.order_confirmed", manifestDefault); !slices.Equal(got, []string{notifications.ChannelInApp}) {
		t.Errorf("overridden type = %v, want [in_app]", got)
	}
	if got := c.DefaultChannels("hr.leave_approved", manifestDefault); !slices.Equal(got, manifestDefault) {
		t.Errorf("unconfigured type = %v, want manifest default %v", got, manifestDefault)
	}
	if !slices.Equal(manifestDefault, []string{notifications.ChannelInApp, notifications.ChannelEmail}) {
		t.Errorf("manifest default mutated to %v", manifestDefault)
	}
}

func TestApplyKillSwitches_RemovesDisabledChannelsOnly(t *testing.T) {
	c := &Config{EmailEnabled: false, SMSEnabled: true, PushEnabled: false}
	in := []string{notifications.ChannelInApp, notifications.ChannelEmail, notifications.ChannelSMS, notifications.ChannelPush}

	got := c.ApplyKillSwitches(in)
	if want := []string{notifications.ChannelInApp, notifications.ChannelSMS}; !slices.Equal(got, want) {
		t.Errorf("ApplyKillSwitches() = %v, want %v", got, want)
	}
	if len(in) != 4 {
		t.Errorf("input mutated to %v", in)
	}
}

func TestValidate(t *testing.T) {
	tests := []struct {
		key   string
		value any
		ok    bool
	}{
		{KeyEmailEnabled, false, true},
		{KeyEmailEnabled, "false", false},
		{KeyEmailProvider, ProviderSMTP, true},
		{KeyEmailProvider, "sendgrid", false},
		{KeySMTPPort, 465, true},
		{KeySMTPPort, float64(465), true},
		{KeySMTPPort, 465.5, false},
		{KeySMTPPort, "465", false},
		{KeyDefaults, map[string][]string{"sales.order_confirmed": {"in_app", "email"}}, true},
		{KeyDefaults, map[string][]string{"sales.order_confirmed": {"fax"}}, false},
		{KeyDefaults, map[string]any{}, false},
	}
	for _, tt := range tests {
		t.Run(fmt.Sprintf("%s=%v", tt.key, tt.value), func(t *testing.T) {
			entry, ok := entryFor(tt.key)
			if !ok {
				t.Fatalf("entryFor(%q) not found", tt.key)
			}
			err := validate(entry, tt.value)
			if (err == nil) != tt.ok {
				t.Fatalf("validate() error = %v, want ok=%v", err, tt.ok)
			}
			if err != nil && !errors.Is(err, ErrInvalidValue) {
				t.Errorf("error %v does not wrap ErrInvalidValue", err)
			}
		})
	}
}

type testEnv struct {
	conn    *sql.DB
	store   *tenantconfig.Store
	service *Service
	tenant  *tenant.Tenant
}

func openTestEnv(t *testing.T) *testEnv {
	t.Helper()
	ctx := t.Context()

	conn, err := db.New(localPostgresDSN)
	if err != nil {
		t.Skipf("postgres not reachable at %s (start compose.dev.yml): %v", localPostgresDSN, err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	tenantStore := tenant.NewStore(conn)
	if err := tenantStore.Bootstrap(ctx); err != nil {
		t.Fatalf("tenant Bootstrap() error: %v", err)
	}
	store := tenantconfig.NewStore(conn)
	if err := store.Bootstrap(ctx); err != nil {
		t.Fatalf("tenantconfig Bootstrap() error: %v", err)
	}

	slug := fmt.Sprintf("notifconfigtest%d", time.Now().UnixNano())
	tt, err := tenantStore.CreateTenant(ctx, slug, "Notif Config Test Co")
	if err != nil {
		t.Fatalf("CreateTenant(%q) error: %v", slug, err)
	}
	t.Cleanup(func() { _, _ = conn.Exec("DELETE FROM system.tenants WHERE id = $1", tt.ID) })

	schema := tenantschema.Name(slug)
	if _, err := conn.ExecContext(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatalf("create tenant schema: %v", err)
	}
	t.Cleanup(func() { _, _ = conn.Exec("DROP SCHEMA IF EXISTS " + schema + " CASCADE") })
	if _, err := conn.ExecContext(ctx, `
		CREATE TABLE `+schema+`.module_config (
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

	resolver := tenantconfig.NewResolver(store, tenantStore, &registry.ModuleRegistry{})
	keys := &rowcrypt.RowKeySet{Active: rowcrypt.RowKey{KeyID: "test-key", Key: make([]byte, 32)}}
	return &testEnv{conn: conn, store: store, service: NewService(resolver, store, keys), tenant: tt}
}

func (e *testEnv) load(t *testing.T) *Config {
	t.Helper()
	cfg, err := e.service.Load(t.Context(), e.tenant.ID)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	return cfg
}

func (e *testEnv) set(t *testing.T, key string, value any) {
	t.Helper()
	if err := e.service.Set(t.Context(), e.tenant.ID, e.tenant.Slug, key, value, ""); err != nil {
		t.Fatalf("Set(%s) error: %v", key, err)
	}
}

func TestLoad_NoConfigEnablesEveryChannel(t *testing.T) {
	env := openTestEnv(t)

	cfg := env.load(t)
	if !cfg.EmailEnabled || !cfg.SMSEnabled || !cfg.PushEnabled {
		t.Errorf("kill switches = email:%v sms:%v push:%v, want all true", cfg.EmailEnabled, cfg.SMSEnabled, cfg.PushEnabled)
	}
	if cfg.Email.Provider != ProviderResend || cfg.Email.SMTP.Port != 587 || !cfg.Email.SMTP.UseTLS {
		t.Errorf("email defaults = %+v", cfg.Email)
	}
	if len(cfg.Defaults) != 0 {
		t.Errorf("Defaults = %v, want empty", cfg.Defaults)
	}
}

func TestSet_EmailKillSwitchDropsEmail(t *testing.T) {
	env := openTestEnv(t)
	env.set(t, KeyEmailEnabled, false)

	cfg := env.load(t)
	if cfg.EmailEnabled {
		t.Fatal("EmailEnabled = true after setting it false")
	}
	userEnabled := []string{notifications.ChannelInApp, notifications.ChannelEmail, notifications.ChannelPush}
	if got := cfg.ApplyKillSwitches(userEnabled); slices.Contains(got, notifications.ChannelEmail) {
		t.Errorf("ApplyKillSwitches() = %v, still contains email", got)
	}
}

func TestSet_OperatorOverrideBeatsTenantValue(t *testing.T) {
	env := openTestEnv(t)
	env.set(t, KeySMSEnabled, true)
	if err := env.store.Set(t.Context(), env.tenant.ID, Namespace+"."+KeySMSEnabled, "false"); err != nil {
		t.Fatalf("operator override Set() error: %v", err)
	}
	env.service.resolver.Invalidate(env.tenant.ID, Namespace+"."+KeySMSEnabled)

	if env.load(t).SMSEnabled {
		t.Error("SMSEnabled = true, want the operator override's false")
	}
}

func TestSet_NotificationDefaultsRoundTrip(t *testing.T) {
	env := openTestEnv(t)
	env.set(t, KeyDefaults, map[string][]string{"sales.order_confirmed": {notifications.ChannelInApp}})

	cfg := env.load(t)
	got := cfg.DefaultChannels("sales.order_confirmed", []string{notifications.ChannelInApp, notifications.ChannelEmail})
	if !slices.Equal(got, []string{notifications.ChannelInApp}) {
		t.Errorf("DefaultChannels() = %v, want [in_app]", got)
	}
}

func TestSet_SecretsStoredEncrypted(t *testing.T) {
	env := openTestEnv(t)
	const apiKey, password = "re_live_secret_api_key", "smtp-secret-password"
	env.set(t, KeyEmailAPIKey, apiKey)
	env.set(t, KeySMTPPassword, password)

	rows, err := env.conn.Query(`SELECT key, value::text, encrypted FROM `+tenantschema.Name(env.tenant.Slug)+`.module_config WHERE module_name = $1`, Namespace)
	if err != nil {
		t.Fatalf("query module_config: %v", err)
	}
	defer func() { _ = rows.Close() }()
	seen := 0
	for rows.Next() {
		var key, value string
		var encrypted bool
		if err := rows.Scan(&key, &value, &encrypted); err != nil {
			t.Fatalf("scan: %v", err)
		}
		seen++
		if strings.Contains(value, apiKey) || strings.Contains(value, password) {
			t.Errorf("module_config %s holds plaintext: %s", key, value)
		}
		if !encrypted {
			t.Errorf("module_config %s encrypted = false", key)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows: %v", err)
	}
	if seen != 2 {
		t.Fatalf("found %d engine module_config rows, want 2", seen)
	}

	cfg := env.load(t)
	if cfg.Email.APIKey != apiKey || cfg.Email.SMTP.Password != password {
		t.Errorf("decrypted api_key=%q password=%q", cfg.Email.APIKey, cfg.Email.SMTP.Password)
	}
}

func TestLoad_PlaintextOverrideForEncryptedKey(t *testing.T) {
	for _, plaintext := range []string{"operator-plaintext", "ab:cd:ef"} {
		t.Run(plaintext, func(t *testing.T) {
			env := openTestEnv(t)
			if err := env.store.Set(t.Context(), env.tenant.ID, Namespace+"."+KeySMTPPassword, plaintext); err != nil {
				t.Fatalf("operator override Set() error: %v", err)
			}

			if got := env.load(t).Email.SMTP.Password; got != plaintext {
				t.Errorf("Password = %q, want the override's plaintext %q", got, plaintext)
			}
		})
	}
}

func (e *testEnv) insertModuleConfig(t *testing.T, key, jsonValue string, encrypted bool) {
	t.Helper()
	query := `INSERT INTO ` + tenantschema.Name(e.tenant.Slug) + `.module_config (module_name, key, value, value_type, encrypted) VALUES ($1, $2, $3, 'string', $4)`
	if _, err := e.conn.ExecContext(t.Context(), query, Namespace, key, jsonValue, encrypted); err != nil {
		t.Fatalf("insert module_config %s: %v", key, err)
	}
}

func TestLoad_UnencryptedModuleConfigRowIsPlaintext(t *testing.T) {
	env := openTestEnv(t)
	env.insertModuleConfig(t, KeyEmailAPIKey, `"seeded:plain:text"`, false)

	if got := env.load(t).Email.APIKey; got != "seeded:plain:text" {
		t.Errorf("APIKey = %q, want the unencrypted row's value", got)
	}
}

func TestLoad_BadEncryptedModuleConfigRowFails(t *testing.T) {
	tests := map[string]string{
		"malformed ciphertext": `"not-ciphertext"`,
		"unknown key id":       `"retired-key:bm9uY2U:Y2lwaGVydGV4dA"`,
	}
	for name, value := range tests {
		t.Run(name, func(t *testing.T) {
			env := openTestEnv(t)
			env.insertModuleConfig(t, KeyEmailAPIKey, value, true)

			if _, err := env.service.Load(t.Context(), env.tenant.ID); err == nil {
				t.Error("Load() succeeded, want a decrypt error")
			}
		})
	}
}

func TestSet_UnknownKeyRejected(t *testing.T) {
	env := openTestEnv(t)
	err := env.service.Set(t.Context(), env.tenant.ID, env.tenant.Slug, "notifications.fax_enabled", true, "")
	if !errors.Is(err, ErrUnknownKey) {
		t.Errorf("Set() error = %v, want ErrUnknownKey", err)
	}
}

func TestSetMany_InvalidValueWritesNothing(t *testing.T) {
	env := openTestEnv(t)
	err := env.service.SetMany(t.Context(), env.tenant.ID, env.tenant.Slug, map[string]any{
		KeyEmailFromName: "Acme",
		KeySMTPPort:      "not a port",
	}, "")
	if !errors.Is(err, ErrInvalidValue) {
		t.Fatalf("SetMany() error = %v, want ErrInvalidValue", err)
	}
	if got := env.load(t).Email.FromName; got != "" {
		t.Errorf("FromName = %q after a rejected SetMany, want nothing written", got)
	}
}

func TestSetMany_NilRemovesTheTenantValue(t *testing.T) {
	env := openTestEnv(t)
	env.set(t, KeyEmailAPIKey, "key-1")
	env.set(t, KeySMTPPort, 2525)

	if err := env.service.SetMany(t.Context(), env.tenant.ID, env.tenant.Slug, map[string]any{
		KeyEmailAPIKey:   nil,
		KeySMTPPort:      nil,
		KeyEmailFromAddr: "noreply@acme.test",
	}, ""); err != nil {
		t.Fatalf("SetMany() error: %v", err)
	}
	cfg := env.load(t)
	if cfg.Email.APIKey != "" || cfg.Email.SMTP.Port != 587 || cfg.Email.FromAddr != "noreply@acme.test" {
		t.Errorf("Email = %+v, want api_key cleared, port back to its default and from_addr set", cfg.Email)
	}
}

func TestLocked_NamesOverriddenKeysOnly(t *testing.T) {
	env := openTestEnv(t)
	env.set(t, KeyPushEnabled, false)
	for _, key := range []string{Namespace + "." + KeySMSEnabled, Namespace + "." + KeyDefaults, "engine.mfa_mode"} {
		if err := env.store.Set(t.Context(), env.tenant.ID, key, "false"); err != nil {
			t.Fatalf("operator override Set(%s) error: %v", key, err)
		}
	}

	locked, err := env.service.Locked(t.Context(), env.tenant.ID)
	if err != nil {
		t.Fatalf("Locked() error: %v", err)
	}
	if want := []string{KeySMSEnabled, KeyDefaults}; !slices.Equal(locked, want) {
		t.Errorf("Locked() = %v, want %v", locked, want)
	}
}
