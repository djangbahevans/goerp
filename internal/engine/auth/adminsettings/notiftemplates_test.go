package adminsettings

import (
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/djangbahevans/goerp/internal/engine/authaudit/audittest"
	"github.com/djangbahevans/goerp/internal/engine/enginenotif"
	"github.com/djangbahevans/goerp/internal/engine/notifconfig"
	"github.com/djangbahevans/goerp/internal/engine/notifications"
	"github.com/djangbahevans/goerp/internal/engine/notiftemplate"
	"github.com/djangbahevans/goerp/internal/engine/route"
)

const templatesPath = "/admin/settings/notification-templates"

// installTemplates installs the fixture modules and gives ft a
// notification_templates table seeded with the engine's defaults and an
// in_app and email default for salesType.
func (e *env) installTemplates(t *testing.T, ft fixtureTenant) {
	t.Helper()
	e.installModules(t, ft)
	ctx := t.Context()
	if err := e.notifs.BootstrapTemplates(ctx, ft.slug); err != nil {
		t.Fatalf("BootstrapTemplates() error: %v", err)
	}
	engineRows, err := enginenotif.DefaultRows()
	if err != nil {
		t.Fatal(err)
	}
	if err := e.notifs.SeedDefaultTemplates(ctx, ft.slug, enginenotif.Module, engineRows); err != nil {
		t.Fatalf("seed engine templates: %v", err)
	}
	if err := e.notifs.SeedDefaultTemplates(ctx, ft.slug, salesModule, []notiftemplate.Row{
		{TemplateKey: salesType, Channel: "in_app", Locale: "en", Fields: map[string]string{notiftemplate.ColTitle: "Order {{.OrderReference}} confirmed"}},
		{TemplateKey: salesType, Channel: "email", Locale: "en", Fields: map[string]string{notiftemplate.ColSubject: "Order {{.OrderReference}}", notiftemplate.ColHTML: "<p>{{.OrderReference}}</p>"}},
	}); err != nil {
		t.Fatalf("seed %s templates: %v", salesModule, err)
	}
}

// doTemplate calls handler for the route naming typ/channel/locale.
func (e *env) doTemplate(t *testing.T, ft fixtureTenant, token string, handler http.HandlerFunc, method, typ, channel, locale, suffix string, body any) *httptest.ResponseRecorder {
	t.Helper()
	params := map[string]string{"type": typ, "channel": channel, "locale": locale}
	withParams := func(w http.ResponseWriter, r *http.Request) {
		handler(w, r.WithContext(route.WithParams(r.Context(), params)))
	}
	return e.do(t, ft, token, withParams, method, templatesPath+"/"+typ+"/"+channel+"/"+locale+suffix, body)
}

func (e *env) listTemplates(t *testing.T, ft fixtureTenant, token string) NotificationTemplateList {
	t.Helper()
	rec := e.do(t, ft, token, e.handler.ServeListNotificationTemplates, http.MethodGet, templatesPath, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("list status = %d, body = %s", rec.Code, rec.Body.String())
	}
	return decode[NotificationTemplateList](t, rec)
}

func auditCount(t *testing.T, e *env, ft fixtureTenant) int {
	t.Helper()
	var n int
	if err := e.conn.QueryRow(`SELECT count(*) FROM system.auth_audit_log WHERE tenant_id = $1 AND event_type = 'tenant.settings_updated'`, ft.id).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestNotificationTemplates_ListsEveryTypeWithItsTemplatesAndCustomisedFlag(t *testing.T) {
	e := newEnv(t)
	ft := e.newTenant(t)
	e.installTemplates(t, ft)
	_, token := e.member(t, ft, "admin")
	if err := e.notifs.SaveTemplateOverride(t.Context(), ft.slug, notiftemplate.Row{TemplateKey: salesType, Channel: "email", Locale: "en", Fields: map[string]string{notiftemplate.ColSubject: "Mine"}}); err != nil {
		t.Fatal(err)
	}

	list := e.listTemplates(t, ft, token)
	i := slices.IndexFunc(list.Types, func(tt TemplateType) bool { return tt.Type == salesType })
	if i < 0 {
		t.Fatalf("types = %+v, want %s", list.Types, salesType)
	}
	sales := list.Types[i]
	want := []TemplateEntry{{Channel: "email", Locale: "en", Customised: true, HasDefault: true}, {Channel: "in_app", Locale: "en", HasDefault: true}}
	if sales.Module != salesModule || sales.Label != "Order Confirmed" || !slices.Equal(sales.Templates, want) || !slices.Equal(sales.AvailableChannels, []string{"in_app", "email", "sms"}) {
		t.Errorf("%s = %+v, want its in_app and customised email templates", salesType, sales)
	}
	for _, nt := range enginenotif.Types {
		j := slices.IndexFunc(list.Types, func(tt TemplateType) bool { return tt.Type == enginenotif.Module+"."+nt.Name })
		if j < 0 || !slices.Contains(list.Types[j].Templates, TemplateEntry{Channel: "in_app", Locale: "en", HasDefault: true}) {
			t.Errorf("engine type %s missing or without its in_app en default", nt.Name)
		}
	}
}

func TestNotificationTemplates_SaveGetAndResetAnOverride(t *testing.T) {
	e := newEnv(t)
	ft := e.newTenant(t)
	e.installTemplates(t, ft)
	admin, token := e.member(t, ft, "admin")

	rec := e.doTemplate(t, ft, token, e.handler.ServePutNotificationTemplate, http.MethodPut, salesType, "in_app", "en", "",
		map[string]string{notiftemplate.ColTitle: "Custom {{.OrderReference}}", notiftemplate.ColBody: ""})
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT status = %d, body = %s", rec.Code, rec.Body.String())
	}
	got := decode[NotificationTemplate](t, rec)
	if got.Default[notiftemplate.ColTitle] != "Order {{.OrderReference}} confirmed" || len(got.Override) != 1 || got.Override[notiftemplate.ColTitle] != "Custom {{.OrderReference}}" {
		t.Errorf("after PUT = %+v, want the default and the override without the empty body", got)
	}
	audittest.AssertLatest(t, e.conn, ft.id, "tenant.settings_updated", "", admin)
	if metadata := latestAuditMetadata(t, e.conn, ft.id); !strings.Contains(metadata, `"notification_templates.`+salesType+`.in_app.en"`) || strings.Contains(metadata, "Custom") {
		t.Errorf("audit metadata = %s, want the template named and its content left out", metadata)
	}

	sent, err := e.notifs.Templates(t.Context(), ft.slug, salesType, []string{"en"})
	if err != nil {
		t.Fatal(err)
	}
	if i := slices.IndexFunc(sent, func(r notifications.StoredTemplate) bool { return r.Channel == "in_app" }); i < 0 || !sent[i].IsOverride {
		t.Errorf("the send lookup = %+v, want the override", sent)
	}

	rec = e.doTemplate(t, ft, token, e.handler.ServeGetNotificationTemplate, http.MethodGet, salesType, "in_app", "en", "", "")
	if got := decode[NotificationTemplate](t, rec); got.Override[notiftemplate.ColTitle] != "Custom {{.OrderReference}}" {
		t.Errorf("GET = %+v, want the saved override", got)
	}
	if list := e.listTemplates(t, ft, token); !slices.ContainsFunc(list.Types, func(tt TemplateType) bool {
		return tt.Type == salesType && slices.Contains(tt.Templates, TemplateEntry{Channel: "in_app", Locale: "en", Customised: true, HasDefault: true})
	}) {
		t.Error("list does not mark the saved in_app template customised")
	}

	before := auditCount(t, e, ft)
	rec = e.doTemplate(t, ft, token, e.handler.ServeDeleteNotificationTemplate, http.MethodDelete, salesType, "in_app", "en", "", "")
	if got := decode[NotificationTemplate](t, rec); rec.Code != http.StatusOK || got.Override != nil || got.Default == nil {
		t.Errorf("DELETE = %d %+v, want the default and no override", rec.Code, got)
	}
	rec = e.doTemplate(t, ft, token, e.handler.ServeDeleteNotificationTemplate, http.MethodDelete, salesType, "in_app", "en", "", "")
	if rec.Code != http.StatusOK {
		t.Errorf("second DELETE status = %d, want 200", rec.Code)
	}
	if n := auditCount(t, e, ft); n != before+1 {
		t.Errorf("audit rows after two resets = %d, want %d: only the reset that deleted an override", n, before+1)
	}
}

func TestNotificationTemplates_OverrideForAChannelOrLocaleWithNoDefault(t *testing.T) {
	e := newEnv(t)
	ft := e.newTenant(t)
	e.installTemplates(t, ft)
	_, token := e.member(t, ft, "admin")

	rec := e.doTemplate(t, ft, token, e.handler.ServePutNotificationTemplate, http.MethodPut, salesType, "sms", "fr-GH", "",
		map[string]string{notiftemplate.ColSMS: "Commande {{.OrderReference}}"})
	got := decode[NotificationTemplate](t, rec)
	if rec.Code != http.StatusOK || got.Default != nil || got.Override[notiftemplate.ColSMS] != "Commande {{.OrderReference}}" {
		t.Errorf("PUT = %d %+v, want an override with no default", rec.Code, got)
	}
	if list := e.listTemplates(t, ft, token); !slices.ContainsFunc(list.Types, func(tt TemplateType) bool {
		return tt.Type == salesType && slices.Contains(tt.Templates, TemplateEntry{Channel: "sms", Locale: "fr-GH", Customised: true})
	}) {
		t.Error("list does not show the fr-GH sms override as customised with no default")
	}
}

func TestNotificationTemplates_GetListsDataSchemaThenEngineVariables(t *testing.T) {
	e := newEnv(t)
	ft := e.newTenant(t)
	e.installTemplates(t, ft)
	_, token := e.member(t, ft, "admin")

	rec := e.doTemplate(t, ft, token, e.handler.ServeGetNotificationTemplate, http.MethodGet, salesType, "sms", "en", "", "")
	got := decode[NotificationTemplate](t, rec)
	if rec.Code != http.StatusOK || got.Default != nil || got.Override != nil {
		t.Fatalf("GET = %d %+v, want no default or override", rec.Code, got)
	}
	want := append([]TemplateVariable{{Name: "OrderReference", Type: "string", Source: "data"}, {Name: "Total", Type: "float", Source: "data"}}, engineVariables...)
	if !slices.Equal(got.Variables, want) {
		t.Errorf("variables = %+v, want %+v", got.Variables, want)
	}

	rec = e.doTemplate(t, ft, token, e.handler.ServeGetNotificationTemplate, http.MethodGet, "engine.activity_due", "in_app", "en", "", "")
	got = decode[NotificationTemplate](t, rec)
	if !slices.Contains(got.Variables, TemplateVariable{Name: "Overdue", Type: "bool", Source: "data"}) || got.Default[notiftemplate.ColTitle] == "" {
		t.Errorf("engine.activity_due = %+v, want its data_schema and embedded default", got)
	}
}

func TestNotificationTemplates_RejectsBadRequestsWithoutWriting(t *testing.T) {
	e := newEnv(t)
	ft := e.newTenant(t)
	e.installTemplates(t, ft)
	_, token := e.member(t, ft, "admin")
	put := func(typ, channel, locale string, body any) (int, errorBody) {
		rec := e.doTemplate(t, ft, token, e.handler.ServePutNotificationTemplate, http.MethodPut, typ, channel, locale, "", body)
		if rec.Code == http.StatusOK {
			return rec.Code, errorBody{}
		}
		return rec.Code, decode[errorBody](t, rec)
	}

	for _, tc := range []struct {
		name                  string
		typ, channel, locale  string
		body                  any
		wantStatus            int
		wantCode, wantDetails string
	}{
		{"unparseable", salesType, "email", "en", map[string]string{notiftemplate.ColSubject: "ok", notiftemplate.ColHTML: "<p>{{.Unclosed"}, http.StatusUnprocessableEntity, "invalid_template", notiftemplate.ColHTML},
		{"another channel's column", salesType, "in_app", "en", map[string]string{notiftemplate.ColSMS: "x"}, http.StatusBadRequest, "invalid_request", ""},
		{"no content", salesType, "in_app", "en", map[string]string{notiftemplate.ColTitle: ""}, http.StatusBadRequest, "invalid_request", ""},
		{"not an object", salesType, "in_app", "en", `["x"]`, http.StatusBadRequest, "invalid_request", ""},
		{"undeclared type", salesModule + ".nope", "in_app", "en", map[string]string{notiftemplate.ColTitle: "x"}, http.StatusNotFound, "not_found", ""},
		{"unavailable channel", salesType, "push", "en", map[string]string{notiftemplate.ColPushTitle: "x"}, http.StatusNotFound, "not_found", ""},
		{"malformed locale", salesType, "in_app", "en_GB!", map[string]string{notiftemplate.ColTitle: "x"}, http.StatusBadRequest, "invalid_request", ""},
		{"non-canonical locale", salesType, "in_app", "en-gb", map[string]string{notiftemplate.ColTitle: "x"}, http.StatusBadRequest, "invalid_request", ""},
	} {
		status, body := put(tc.typ, tc.channel, tc.locale, tc.body)
		if status != tc.wantStatus || body.Error.Code != tc.wantCode || (tc.wantDetails != "" && body.Error.Details["field"] != tc.wantDetails) {
			t.Errorf("%s: PUT = %d %+v, want %d %s %s", tc.name, status, body, tc.wantStatus, tc.wantCode, tc.wantDetails)
		}
	}
	if variants, err := e.notifs.TemplateVariants(t.Context(), ft.slug); err != nil || slices.ContainsFunc(variants, func(v notifications.TemplateVariant) bool { return v.HasOverride }) {
		t.Errorf("variants = %+v, %v, want no override written", variants, err)
	}
}

func TestNotificationTemplates_PreviewRendersSampleDataAndCountsSMS(t *testing.T) {
	e := newEnv(t)
	ft := e.newTenant(t)
	e.installTemplates(t, ft)
	_, token := e.member(t, ft, "admin")
	preview := func(channel string, body any) (int, TemplatePreview, errorBody) {
		rec := e.doTemplate(t, ft, token, e.handler.ServePreviewNotificationTemplate, http.MethodPost, salesType, channel, "en", "/preview", body)
		if rec.Code == http.StatusOK {
			return rec.Code, decode[TemplatePreview](t, rec), errorBody{}
		}
		return rec.Code, TemplatePreview{}, decode[errorBody](t, rec)
	}

	status, got, _ := preview("sms", map[string]any{
		"template": map[string]string{notiftemplate.ColSMS: "  {{.TenantName}}: {{.OrderReference}} for {{.Total}}  "},
		"data":     map[string]any{"OrderReference": "SO-0042"},
	})
	if status != http.StatusOK || got.Rendered[notiftemplate.ColSMS] != "Settings Test Co: SO-0042 for 1.5" || got.SMS == nil || *got.SMS != (SMSLength{Characters: 33, Segments: 1}) {
		t.Errorf("sms preview = %d %+v %+v", status, got, got.SMS)
	}

	status, got, _ = preview("in_app", map[string]any{"template": map[string]string{
		notiftemplate.ColTitle: "{{.OrderReference}}", notiftemplate.ColActionURL: "{{.ActionURL}}orders",
	}})
	wantURL := "http://" + ft.slug + ".goerp.test:5173/orders"
	if status != http.StatusOK || got.Rendered[notiftemplate.ColTitle] != "OrderReference" || got.Rendered[notiftemplate.ColActionURL] != wantURL || got.SMS != nil {
		t.Errorf("in_app preview = %d %+v, want the field name as sample data and the tenant's URL", status, got)
	}

	if err := e.notif.SetMany(t.Context(), ft.id, ft.slug, map[string]any{notifconfig.KeyEmailLayout: themeModule + "/emails/layout.html"}, ""); err != nil {
		t.Fatal(err)
	}
	status, got, _ = preview("email", map[string]any{"template": map[string]string{notiftemplate.ColHTML: "<p>{{.OrderReference}}</p>"}})
	if html := got.Rendered[notiftemplate.ColHTML]; status != http.StatusOK || !strings.HasPrefix(html, "<html><body>Settings Test Co: <p>OrderReference</p>") {
		t.Errorf("email preview = %d %q, want the body in the tenant's layout", status, html)
	}

	status, _, errBody := preview("sms", map[string]any{"template": map[string]string{notiftemplate.ColSMS: "{{index .Missing 3}}"}})
	if status != http.StatusUnprocessableEntity || errBody.Error.Code != "invalid_template" || errBody.Error.Details["field"] != notiftemplate.ColSMS {
		t.Errorf("failing preview = %d %+v, want invalid_template on sms_template", status, errBody)
	}
	if variants, _ := e.notifs.TemplateVariants(t.Context(), ft.slug); slices.ContainsFunc(variants, func(v notifications.TemplateVariant) bool { return v.Channel == "sms" }) {
		t.Error("a preview wrote a template row")
	}
}

func TestNotificationTemplates_RequireTheAdminRole(t *testing.T) {
	e := newEnv(t)
	ft := e.newTenant(t)
	e.installTemplates(t, ft)
	_, token := e.member(t, ft, "user")

	body := map[string]string{notiftemplate.ColTitle: "x"}
	for name, rec := range map[string]*httptest.ResponseRecorder{
		"list":    e.do(t, ft, token, e.handler.ServeListNotificationTemplates, http.MethodGet, templatesPath, ""),
		"get":     e.doTemplate(t, ft, token, e.handler.ServeGetNotificationTemplate, http.MethodGet, salesType, "in_app", "en", "", ""),
		"put":     e.doTemplate(t, ft, token, e.handler.ServePutNotificationTemplate, http.MethodPut, salesType, "in_app", "en", "", body),
		"delete":  e.doTemplate(t, ft, token, e.handler.ServeDeleteNotificationTemplate, http.MethodDelete, salesType, "in_app", "en", "", ""),
		"preview": e.doTemplate(t, ft, token, e.handler.ServePreviewNotificationTemplate, http.MethodPost, salesType, "in_app", "en", "/preview", map[string]any{"template": body}),
	} {
		if rec.Code != http.StatusForbidden {
			t.Errorf("%s status = %d, want 403", name, rec.Code)
		}
	}
}

func TestSMSLength(t *testing.T) {
	for _, tc := range []struct {
		text string
		want SMSLength
	}{
		{"", SMSLength{0, 1}},
		{strings.Repeat("a", 160), SMSLength{160, 1}},
		{strings.Repeat("a", 161), SMSLength{161, 2}},
		{strings.Repeat("a", 306), SMSLength{306, 2}},
		{strings.Repeat("a", 307), SMSLength{307, 3}},
		{"Total €5 {ok}", SMSLength{13, 1}},
		{strings.Repeat("é", 70), SMSLength{70, 1}},
		{"GH₵" + strings.Repeat("a", 67), SMSLength{70, 1}},
		{"GH₵" + strings.Repeat("a", 68), SMSLength{71, 2}},
	} {
		if got := smsLength(tc.text); *got != tc.want {
			t.Errorf("smsLength(%.20q…) = %+v, want %+v", tc.text, *got, tc.want)
		}
	}
}
