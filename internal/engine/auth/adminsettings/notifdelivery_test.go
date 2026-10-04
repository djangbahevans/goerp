package adminsettings

import (
	"database/sql"
	"encoding/json/v2"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/djangbahevans/goerp/internal/engine/authaudit/audittest"
	"github.com/djangbahevans/goerp/internal/engine/manifest"
	"github.com/djangbahevans/goerp/internal/engine/module"
	"github.com/djangbahevans/goerp/internal/engine/notifconfig"
	"github.com/djangbahevans/goerp/internal/engine/notify"
	"github.com/djangbahevans/goerp/internal/engine/tenantschema"
)

const (
	deliveryPath  = "/admin/settings/notification-delivery"
	testEmailPath = deliveryPath + "/test-email"

	salesModule = "deliverytest"
	salesType   = salesModule + ".order_confirmed"
	themeModule = "deliverytheme"
)

// installModules loads a module declaring salesType and a theme module
// with emails/layout.html, and enables the former for ft.
func (e *env) installModules(t *testing.T, ft fixtureTenant) {
	t.Helper()
	themeDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(themeDir, "emails"), 0o755); err != nil {
		t.Fatal(err)
	}
	layout := `<html><body>{{.TenantName}}: {{.Content}}<a href="{{.UnsubscribeURL}}">x</a></body></html>`
	if err := os.WriteFile(filepath.Join(themeDir, "emails", "layout.html"), []byte(layout), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(themeDir, "emails", "broken.html"), []byte(`<html>{{template "missing"}}</html>`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := e.modules.Update(map[string]*module.LoadedModule{
		salesModule: {Status: module.StatusReady, Manifest: manifest.Manifest{Name: salesModule, Type: "standard", NotificationTypes: []manifest.NotificationType{{
			Name: "order_confirmed", Label: "Order Confirmed",
			DefaultChannels:   []string{"in_app", "email"},
			AvailableChannels: []string{"in_app", "email", "sms"},
			DataSchema:        map[string]string{"OrderReference": "string", "Total": "float"},
		}}}},
		themeModule: {Status: module.StatusReady, PackagePath: themeDir, Manifest: manifest.Manifest{Name: themeModule, Type: "theme"}},
	}); err != nil {
		t.Fatalf("registry Update() error: %v", err)
	}
	if err := e.billing.UpsertEntitlementOverride(t.Context(), ft.id, "module."+salesModule, "true", nil, nil, nil); err != nil {
		t.Fatalf("UpsertEntitlementOverride() error: %v", err)
	}
}

func (e *env) getDelivery(t *testing.T, ft fixtureTenant, token string) NotificationDelivery {
	t.Helper()
	rec := e.do(t, ft, token, e.handler.ServeGetNotificationDelivery, http.MethodGet, deliveryPath, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET status = %d, body = %s", rec.Code, rec.Body.String())
	}
	return decode[NotificationDelivery](t, rec)
}

func (e *env) patchDelivery(t *testing.T, ft fixtureTenant, token string, body any) (int, NotificationDelivery, errorBody) {
	t.Helper()
	rec := e.do(t, ft, token, e.handler.ServePatchNotificationDelivery, http.MethodPatch, deliveryPath, body)
	if rec.Code == http.StatusOK {
		return rec.Code, decode[NotificationDelivery](t, rec), errorBody{}
	}
	return rec.Code, NotificationDelivery{}, decode[errorBody](t, rec)
}

func (e *env) loadNotif(t *testing.T, ft fixtureTenant) *notifconfig.Config {
	t.Helper()
	cfg, err := e.notif.Load(t.Context(), ft.id)
	if err != nil {
		t.Fatalf("notifconfig Load() error: %v", err)
	}
	return cfg
}

func TestNotificationDelivery_GetDefaults(t *testing.T) {
	e := newEnv(t)
	ft := e.newTenant(t)
	e.installModules(t, ft)
	_, token := e.member(t, ft, "admin")

	got := e.getDelivery(t, ft, token)
	if got.Channels != (DeliveryChannels{EmailEnabled: true, SMSEnabled: true, PushEnabled: true}) {
		t.Errorf("channels = %+v, want all enabled", got.Channels)
	}
	wantEmail := DeliveryEmail{Provider: "resend", SMTP: DeliverySMTP{Port: 587, UseTLS: true}}
	if got.Email != wantEmail {
		t.Errorf("email = %+v, want %+v", got.Email, wantEmail)
	}
	if got.SMS.SenderID != "" || len(got.Defaults) != 0 || len(got.Locked) != 0 {
		t.Errorf("sms = %+v, defaults = %v, locked = %v, want empty", got.SMS, got.Defaults, got.Locked)
	}

	i := slices.IndexFunc(got.Types, func(dt DeliveryType) bool { return dt.Type == salesType })
	if i < 0 {
		t.Fatalf("types = %+v, want %s", got.Types, salesType)
	}
	want := DeliveryType{Type: salesType, Module: salesModule, Label: "Order Confirmed", DefaultChannels: []string{"in_app", "email"}, AvailableChannels: []string{"in_app", "email", "sms"}}
	if gotJSON, wantJSON := mustJSON(t, got.Types[i]), mustJSON(t, want); gotJSON != wantJSON {
		t.Errorf("type = %s, want %s", gotJSON, wantJSON)
	}
	for _, nt := range notify.EngineTypes {
		if !slices.ContainsFunc(got.Types, func(dt DeliveryType) bool { return dt.Type == notify.EngineModule+"."+nt.Name }) {
			t.Errorf("types missing engine type %s", nt.Name)
		}
	}
	if slices.ContainsFunc(got.Types, func(dt DeliveryType) bool { return dt.Module == themeModule }) {
		t.Errorf("types = %+v, include the theme module", got.Types)
	}
}

func TestNotificationDelivery_TypesSkipModulesNotEnabledForTheTenant(t *testing.T) {
	e := newEnv(t)
	ft := e.newTenant(t)
	e.installModules(t, ft)
	other := e.newTenant(t)
	_, token := e.member(t, other, "admin")

	got := e.getDelivery(t, other, token)
	if slices.ContainsFunc(got.Types, func(dt DeliveryType) bool { return dt.Type == salesType }) {
		t.Errorf("types include %s, whose module the tenant does not have", salesType)
	}
	code, _, body := e.patchDelivery(t, other, token, map[string]any{"defaults": map[string]any{salesType: []string{"in_app"}}})
	if code != http.StatusUnprocessableEntity || body.Error.Details["field"] != "defaults."+salesType {
		t.Errorf("PATCH = %d %+v, want 422 on defaults.%s", code, body, salesType)
	}
}

func TestNotificationDelivery_PatchRoundTripsToTheSendPath(t *testing.T) {
	e := newEnv(t)
	ft := e.newTenant(t)
	e.installModules(t, ft)
	admin, token := e.member(t, ft, "admin")

	const apiKey = "re_live_secret_value"
	code, got, errBody := e.patchDelivery(t, ft, token, map[string]any{
		"channels": map[string]any{"sms_enabled": false},
		"email": map[string]any{
			"provider": "smtp", "api_key": apiKey, "from_name": " Acme Corp ", "from_addr": "noreply@acme.test",
			"reply_to": "support@acme.test", "layout_template": themeModule + "/emails/layout.html",
			"smtp": map[string]any{"host": "smtp.acme.test", "port": 2525, "user": "mailer", "password": "smtp-secret", "use_tls": false},
		},
		"sms":      map[string]any{"sender_id": "AcmeCorp"},
		"defaults": map[string]any{salesType: []string{"in_app", "sms", "in_app"}},
	})
	if code != http.StatusOK {
		t.Fatalf("PATCH status = %d, body = %+v", code, errBody)
	}

	wantEmail := DeliveryEmail{
		Provider: "smtp", APIKey: secretMask, FromName: "Acme Corp", FromAddr: "noreply@acme.test", ReplyTo: "support@acme.test",
		LayoutTemplate: themeModule + "/emails/layout.html",
		SMTP:           DeliverySMTP{Host: "smtp.acme.test", Port: 2525, User: "mailer", Password: secretMask, UseTLS: false},
	}
	if got.Email != wantEmail {
		t.Errorf("PATCH email = %+v, want %+v", got.Email, wantEmail)
	}
	if again := e.getDelivery(t, ft, token); mustJSON(t, again) != mustJSON(t, got) {
		t.Errorf("GET after PATCH = %s, want the PATCH response %s", mustJSON(t, again), mustJSON(t, got))
	}

	cfg := e.loadNotif(t, ft)
	if cfg.SMSEnabled || !cfg.EmailEnabled || cfg.Email.APIKey != apiKey || cfg.Email.SMTP.Password != "smtp-secret" || cfg.Email.SMTP.Port != 2525 || cfg.SMS.SenderID != "AcmeCorp" {
		t.Errorf("Load() = %+v, want the saved values", cfg)
	}
	if ch := cfg.DefaultChannels(salesType, nil); !slices.Equal(ch, []string{"in_app", "sms"}) {
		t.Errorf("DefaultChannels(%s) = %v, want [in_app sms]", salesType, ch)
	}

	var stored string
	if err := e.conn.QueryRow(fmt.Sprintf(`SELECT value #>> '{}' FROM %s.module_config WHERE module_name = 'engine' AND key = $1`, tenantschema.Name(ft.slug)), notifconfig.KeyEmailAPIKey).Scan(&stored); err != nil {
		t.Fatalf("read stored api_key: %v", err)
	}
	if strings.Contains(stored, apiKey) {
		t.Error("module_config holds the API key in plaintext")
	}

	audittest.AssertLatest(t, e.conn, ft.id, "tenant.settings_updated", "", admin)
	metadata := latestAuditMetadata(t, e.conn, ft.id)
	if strings.Contains(metadata, apiKey) || strings.Contains(metadata, "smtp-secret") {
		t.Errorf("audit metadata %s records a secret", metadata)
	}
	for _, field := range []string{"channels.sms_enabled", "email.api_key", "email.smtp.password", "sms.sender_id", "defaults." + salesType} {
		if !strings.Contains(metadata, `"`+field+`"`) {
			t.Errorf("audit metadata %s does not name %s", metadata, field)
		}
	}
}

func TestNotificationDelivery_Secrets(t *testing.T) {
	e := newEnv(t)
	ft := e.newTenant(t)
	_, token := e.member(t, ft, "admin")

	if code, _, body := e.patchDelivery(t, ft, token, map[string]any{"email": map[string]any{"api_key": "key-1"}}); code != http.StatusOK {
		t.Fatalf("set api_key = %d %+v", code, body)
	}
	for _, body := range []map[string]any{
		{"email": map[string]any{"api_key": secretMask, "from_name": "Acme"}},
		{"email": map[string]any{"from_name": "Acme Two"}},
	} {
		code, got, errBody := e.patchDelivery(t, ft, token, body)
		if code != http.StatusOK || got.Email.APIKey != secretMask {
			t.Fatalf("PATCH %v = %d %+v %+v, want the key kept", body, code, got.Email, errBody)
		}
		if key := e.loadNotif(t, ft).Email.APIKey; key != "key-1" {
			t.Fatalf("after PATCH %v api_key = %q, want key-1", body, key)
		}
	}

	code, got, _ := e.patchDelivery(t, ft, token, map[string]any{"email": map[string]any{"api_key": ""}})
	if code != http.StatusOK || got.Email.APIKey != "" {
		t.Fatalf("clear api_key = %d %+v", code, got.Email)
	}
	if key := e.loadNotif(t, ft).Email.APIKey; key != "" {
		t.Errorf("api_key after clearing = %q, want empty", key)
	}
}

func TestNotificationDelivery_InvalidFieldWritesNothing(t *testing.T) {
	e := newEnv(t)
	ft := e.newTenant(t)
	_, token := e.member(t, ft, "admin")

	code, _, body := e.patchDelivery(t, ft, token, map[string]any{
		"channels": map[string]any{"push_enabled": false},
		"email":    map[string]any{"from_name": "Acme", "api_key": "new-key", "from_addr": "not an address"},
	})
	if code != http.StatusUnprocessableEntity || body.Error.Code != "invalid_setting" || body.Error.Details["field"] != "email.from_addr" {
		t.Fatalf("PATCH = %d %+v, want 422 invalid_setting on email.from_addr", code, body)
	}
	cfg := e.loadNotif(t, ft)
	if !cfg.PushEnabled || cfg.Email.FromName != "" || cfg.Email.APIKey != "" {
		t.Errorf("Load() after a rejected PATCH = %+v, want nothing written", cfg)
	}
}

func TestNotificationDelivery_RejectsInvalidValues(t *testing.T) {
	e := newEnv(t)
	ft := e.newTenant(t)
	e.installModules(t, ft)
	_, token := e.member(t, ft, "admin")

	for _, tc := range []struct {
		field string
		body  map[string]any
	}{
		{"email.provider", map[string]any{"email": map[string]any{"provider": "sendgrid"}}},
		{"email.from_name", map[string]any{"email": map[string]any{"from_name": strings.Repeat("a", 201)}}},
		{"email.from_addr", map[string]any{"email": map[string]any{"from_addr": ""}}},
		{"email.from_addr", map[string]any{"email": map[string]any{"from_addr": "Acme <noreply@acme.test>"}}},
		{"email.reply_to", map[string]any{"email": map[string]any{"reply_to": "support"}}},
		{"email.layout_template", map[string]any{"email": map[string]any{"layout_template": themeModule + "/emails/missing.html"}}},
		{"email.layout_template", map[string]any{"email": map[string]any{"layout_template": salesModule + "/emails/layout.html"}}},
		{"email.smtp.host", map[string]any{"email": map[string]any{"smtp": map[string]any{"host": "smtp host"}}}},
		{"email.smtp.port", map[string]any{"email": map[string]any{"smtp": map[string]any{"port": 0}}}},
		{"email.smtp.port", map[string]any{"email": map[string]any{"smtp": map[string]any{"port": 65536}}}},
		{"sms.sender_id", map[string]any{"sms": map[string]any{"sender_id": "AcmeCorporation"}}},
		{"sms.sender_id", map[string]any{"sms": map[string]any{"sender_id": "Acme-Corp"}}},
		{"defaults.sales.unknown", map[string]any{"defaults": map[string]any{"sales.unknown": []string{"in_app"}}}},
		{"defaults." + salesType, map[string]any{"defaults": map[string]any{salesType: []string{"push"}}}},
	} {
		code, _, body := e.patchDelivery(t, ft, token, tc.body)
		if code != http.StatusUnprocessableEntity || body.Error.Code != "invalid_setting" || body.Error.Details["field"] != tc.field {
			t.Errorf("PATCH %v = %d %+v, want 422 invalid_setting on %s", tc.body, code, body, tc.field)
		}
	}

	for _, valid := range []string{"", "Acme123", "+233201234567"} {
		if code, _, body := e.patchDelivery(t, ft, token, map[string]any{"sms": map[string]any{"sender_id": valid}}); code != http.StatusOK {
			t.Errorf("sender_id %q = %d %+v, want 200", valid, code, body)
		}
	}
	for _, valid := range []string{"10.0.0.5", "::1", "mail.example.com"} {
		if code, _, body := e.patchDelivery(t, ft, token, map[string]any{"email": map[string]any{"smtp": map[string]any{"host": valid}}}); code != http.StatusOK {
			t.Errorf("smtp.host %q = %d %+v, want 200", valid, code, body)
		}
	}
}

func TestNotificationDelivery_RejectsMalformedBodies(t *testing.T) {
	e := newEnv(t)
	ft := e.newTenant(t)
	_, token := e.member(t, ft, "admin")

	for _, body := range []string{
		`{"email": {"unknown": 1}}`,
		`{"locked": []}`,
		`{"types": []}`,
		`{"email": {"smtp": {"port": "587"}}}`,
		`not json`,
	} {
		rec := e.do(t, ft, token, e.handler.ServePatchNotificationDelivery, http.MethodPatch, deliveryPath, body)
		if got := decode[errorBody](t, rec); rec.Code != http.StatusBadRequest || got.Error.Code != "invalid_request" {
			t.Errorf("PATCH %s = %d %s, want 400 invalid_request", body, rec.Code, rec.Body.String())
		}
	}
}

func TestNotificationDelivery_DefaultsMergeByType(t *testing.T) {
	e := newEnv(t)
	ft := e.newTenant(t)
	e.installModules(t, ft)
	_, token := e.member(t, ft, "admin")
	engineType := notify.EngineModule + "." + notify.EngineTypes[0].Name

	if code, _, body := e.patchDelivery(t, ft, token, map[string]any{"defaults": map[string]any{
		salesType: []string{"email"}, engineType: []string{"in_app"},
	}}); code != http.StatusOK {
		t.Fatalf("set defaults = %d %+v", code, body)
	}
	code, got, body := e.patchDelivery(t, ft, token, map[string]any{"defaults": map[string]any{salesType: nil}})
	if code != http.StatusOK {
		t.Fatalf("reset %s = %d %+v", salesType, code, body)
	}
	if want := map[string][]string{engineType: {"in_app"}}; mustJSON(t, got.Defaults) != mustJSON(t, want) {
		t.Errorf("defaults = %v, want %v", got.Defaults, want)
	}

	code, got, _ = e.patchDelivery(t, ft, token, map[string]any{"defaults": map[string]any{engineType: nil}})
	if code != http.StatusOK || len(got.Defaults) != 0 {
		t.Errorf("reset every type = %d %v, want no defaults", code, got.Defaults)
	}
}

func TestNotificationDelivery_OperatorOverrideLocksAField(t *testing.T) {
	e := newEnv(t)
	ft := e.newTenant(t)
	_, token := e.member(t, ft, "admin")
	if err := e.config.Set(t.Context(), ft.id, notifconfig.Namespace+"."+notifconfig.KeySMSEnabled, "false"); err != nil {
		t.Fatalf("operator override Set() error: %v", err)
	}

	got := e.getDelivery(t, ft, token)
	if got.Channels.SMSEnabled || !slices.Equal(got.Locked, []string{"channels.sms_enabled"}) {
		t.Fatalf("GET channels = %+v locked = %v, want sms off and locked", got.Channels, got.Locked)
	}

	code, _, body := e.patchDelivery(t, ft, token, map[string]any{"channels": map[string]any{"sms_enabled": true, "push_enabled": false}})
	if code != http.StatusUnprocessableEntity || body.Error.Code != "locked_setting" || body.Error.Details["field"] != "channels.sms_enabled" {
		t.Fatalf("PATCH sms_enabled = %d %+v, want 422 locked_setting", code, body)
	}
	if !e.loadNotif(t, ft).PushEnabled {
		t.Error("push_enabled written by a PATCH rejected as locked")
	}

	if code, _, body := e.patchDelivery(t, ft, token, map[string]any{"channels": map[string]any{"sms_enabled": false, "push_enabled": false}}); code != http.StatusOK {
		t.Errorf("PATCH resending the locked value = %d %+v, want 200", code, body)
	}
}

func TestNotificationDelivery_AdminRoleRequired(t *testing.T) {
	e := newEnv(t)
	ft := e.newTenant(t)
	_, token := e.member(t, ft, "user")

	for _, tc := range []struct {
		method, path string
		handler      http.HandlerFunc
	}{
		{http.MethodGet, deliveryPath, e.handler.ServeGetNotificationDelivery},
		{http.MethodPatch, deliveryPath, e.handler.ServePatchNotificationDelivery},
		{http.MethodPost, testEmailPath, e.handler.ServeTestEmail},
	} {
		rec := e.do(t, ft, token, tc.handler, tc.method, tc.path, map[string]any{})
		if got := decode[errorBody](t, rec); rec.Code != http.StatusForbidden || got.Error.Code != "forbidden" {
			t.Errorf("%s %s = %d %s, want 403 forbidden", tc.method, tc.path, rec.Code, rec.Body.String())
		}
	}
}

func TestTestEmail_SendsUnsavedSettingsToTheAdmin(t *testing.T) {
	e := newEnv(t)
	e.handler.deps.TestEmail.(*notify.EmailTester).SMTPAllowPrivateHosts = true
	ft := e.newTenant(t)
	e.installModules(t, ft)
	adminID, token := e.member(t, ft, "admin")
	var adminEmail string
	if err := e.conn.QueryRow(`SELECT email FROM system.users WHERE id = $1`, adminID).Scan(&adminEmail); err != nil {
		t.Fatalf("read admin email: %v", err)
	}
	if code, _, body := e.patchDelivery(t, ft, token, map[string]any{"email": map[string]any{"api_key": "saved-key", "from_addr": "saved@acme.test"}}); code != http.StatusOK {
		t.Fatalf("save settings = %d %+v", code, body)
	}
	before := mustJSON(t, e.loadNotif(t, ft))

	rec := e.do(t, ft, token, e.handler.ServeTestEmail, http.MethodPost, testEmailPath, map[string]any{"email": map[string]any{
		"provider": "smtp", "api_key": secretMask, "from_addr": "test@acme.test",
		"layout_template": themeModule + "/emails/layout.html",
		"smtp":            map[string]any{"host": "localhost", "port": 1025, "use_tls": false},
	}})
	if rec.Code != http.StatusOK {
		t.Fatalf("test email = %d %s", rec.Code, rec.Body.String())
	}
	if got := decode[TestEmailResult](t, rec); got != (TestEmailResult{SentTo: adminEmail, Provider: "smtp"}) {
		t.Errorf("test email = %+v, want sent to %s through smtp", got, adminEmail)
	}

	resp, err := http.Get("http://localhost:8025/api/v1/messages?query=" + url.QueryEscape("to:"+adminEmail))
	if err != nil {
		t.Fatalf("query mailpit API: %v", err)
	}
	defer resp.Body.Close()
	type address struct {
		Address string `json:"Address"`
	}
	type message struct {
		Subject string    `json:"Subject"`
		From    address   `json:"From"`
		To      []address `json:"To"`
	}
	var found struct {
		Messages []message `json:"messages"`
	}
	if err := json.UnmarshalRead(resp.Body, &found); err != nil {
		t.Fatalf("decode mailpit response: %v", err)
	}
	// Mailpit's to: search also matches other recipients on the same domain.
	toAdmin := slices.DeleteFunc(found.Messages, func(m message) bool {
		return !slices.ContainsFunc(m.To, func(a address) bool { return a.Address == adminEmail })
	})
	if len(toAdmin) != 1 || toAdmin[0].From.Address != "test@acme.test" || !strings.HasPrefix(toAdmin[0].Subject, "Test email from") {
		t.Errorf("mailpit messages to %s = %+v, want one test email from test@acme.test", adminEmail, toAdmin)
	}

	if after := mustJSON(t, e.loadNotif(t, ft)); after != before {
		t.Errorf("saved settings changed by a test email:\n%s\nwant\n%s", after, before)
	}
	var audits int
	if err := e.conn.QueryRow(`SELECT count(*) FROM system.auth_audit_log WHERE tenant_id = $1 AND event_type = 'tenant.settings_updated'`, ft.id).Scan(&audits); err != nil {
		t.Fatalf("count audit rows: %v", err)
	}
	if audits != 1 {
		t.Errorf("settings_updated audit rows = %d, want only the PATCH's", audits)
	}
}

func TestTestEmail_ProviderFailureAndRateLimit(t *testing.T) {
	e := newEnv(t)
	e.handler.deps.TestEmail.(*notify.EmailTester).SMTPAllowPrivateHosts = true
	ft := e.newTenant(t)
	_, token := e.member(t, ft, "admin")
	body := map[string]any{"email": map[string]any{
		"provider": "smtp", "from_addr": "test@acme.test",
		"smtp": map[string]any{"host": "127.0.0.1", "port": 1, "use_tls": false},
	}}

	for i := range testEmailLimit {
		rec := e.do(t, ft, token, e.handler.ServeTestEmail, http.MethodPost, testEmailPath, body)
		got := decode[struct {
			Error struct {
				Code    string            `json:"code"`
				Details map[string]string `json:"details"`
			} `json:"error"`
		}](t, rec)
		if rec.Code != http.StatusBadGateway || got.Error.Code != "test_email_failed" || got.Error.Details["message"] != "could not connect to the email provider" {
			t.Fatalf("attempt %d = %d %s, want 502 test_email_failed with a generic connection message", i+1, rec.Code, rec.Body.String())
		}
	}
	rec := e.do(t, ft, token, e.handler.ServeTestEmail, http.MethodPost, testEmailPath, body)
	if got := decode[errorBody](t, rec); rec.Code != http.StatusTooManyRequests || got.Error.Code != "rate_limit_exceeded" || rec.Header().Get("Retry-After") == "" {
		t.Errorf("attempt %d = %d %s, want 429 rate_limit_exceeded with Retry-After", testEmailLimit+1, rec.Code, rec.Body.String())
	}
}

func TestTestEmail_RejectsBlockedSMTPDestinations(t *testing.T) {
	e := newEnv(t)
	ft := e.newTenant(t)
	_, token := e.member(t, ft, "admin")
	before := mustJSON(t, e.loadNotif(t, ft))

	for _, host := range []string{"localhost", "::ffff:127.0.0.1", "10.0.0.1", "169.254.169.254", "fd00:ec2::254"} {
		rec := e.do(t, ft, token, e.handler.ServeTestEmail, http.MethodPost, testEmailPath, map[string]any{"email": map[string]any{
			"provider":  "smtp",
			"from_addr": "test@acme.test",
			"smtp":      map[string]any{"host": host, "port": 1025, "use_tls": false},
		}})
		got := decode[errorBody](t, rec)
		if rec.Code != http.StatusBadGateway || got.Error.Code != "test_email_failed" || got.Error.Details["message"] != "could not connect to the email provider" {
			t.Fatalf("test email to %s = %d %s, want generic 502 test_email_failed", host, rec.Code, rec.Body.String())
		}
	}

	if after := mustJSON(t, e.loadNotif(t, ft)); after != before {
		t.Fatalf("blocked test email changed saved settings: %s, want %s", after, before)
	}
}

func TestTestEmail_PreservesProviderRejection(t *testing.T) {
	e := newEnv(t)
	ft := e.newTenant(t)
	_, token := e.member(t, ft, "admin")
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = w.Write([]byte(`{"name":"validation_error","message":"sender domain is not verified"}`))
	}))
	t.Cleanup(provider.Close)
	e.handler.deps.TestEmail.(*notify.EmailTester).ResendBaseURL = provider.URL

	rec := e.do(t, ft, token, e.handler.ServeTestEmail, http.MethodPost, testEmailPath, map[string]any{"email": map[string]any{
		"provider":  "resend",
		"api_key":   "test-key",
		"from_addr": "test@acme.test",
	}})
	got := decode[errorBody](t, rec)
	if rec.Code != http.StatusBadGateway || got.Error.Code != "test_email_failed" || !strings.Contains(got.Error.Details["message"], "sender domain is not verified") {
		t.Fatalf("provider rejection = %d %s, want 502 with the provider's message", rec.Code, rec.Body.String())
	}
}

func TestTestEmail_RejectsIncompleteSettings(t *testing.T) {
	e := newEnv(t)
	ft := e.newTenant(t)
	e.installModules(t, ft)
	_, token := e.member(t, ft, "admin")

	for _, tc := range []struct {
		field string
		email map[string]any
	}{
		{"email.from_addr", map[string]any{"api_key": "key"}},
		{"email.api_key", map[string]any{"from_addr": "test@acme.test"}},
		{"email.smtp.host", map[string]any{"provider": "smtp", "from_addr": "test@acme.test"}},
		{"email.smtp.port", map[string]any{"provider": "smtp", "smtp": map[string]any{"port": 0}}},
		{"email.layout_template", map[string]any{
			"provider": "smtp", "from_addr": "test@acme.test", "layout_template": themeModule + "/emails/broken.html",
			"smtp": map[string]any{"host": "127.0.0.1", "port": 1},
		}},
	} {
		rec := e.do(t, ft, token, e.handler.ServeTestEmail, http.MethodPost, testEmailPath, map[string]any{"email": tc.email})
		if got := decode[errorBody](t, rec); rec.Code != http.StatusUnprocessableEntity || got.Error.Details["field"] != tc.field {
			t.Errorf("test email %v = %d %s, want 422 on %s", tc.email, rec.Code, rec.Body.String(), tc.field)
		}
	}
}

func latestAuditMetadata(t *testing.T, conn *sql.DB, tenantID string) string {
	t.Helper()
	var metadata string
	err := conn.QueryRow(`
		SELECT metadata::text FROM system.auth_audit_log
		WHERE tenant_id = $1 AND event_type = 'tenant.settings_updated'
		ORDER BY created_at DESC LIMIT 1
	`, tenantID).Scan(&metadata)
	if err != nil {
		t.Fatalf("read audit metadata: %v", err)
	}
	return metadata
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v, json.Deterministic(true))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(b)
}
