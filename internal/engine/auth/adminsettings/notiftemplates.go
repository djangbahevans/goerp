package adminsettings

import (
	"bytes"
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	htmltemplate "html/template"
	"maps"
	"net/http"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/djangbahevans/goerp/internal/engine/httperr"
	"github.com/djangbahevans/goerp/internal/engine/l10n"
	"github.com/djangbahevans/goerp/internal/engine/mailer"
	"github.com/djangbahevans/goerp/internal/engine/notiftemplate"
	"github.com/djangbahevans/goerp/internal/engine/notify"
	"github.com/djangbahevans/goerp/internal/engine/route"
	tenantresolve "github.com/djangbahevans/goerp/internal/engine/tenant/resolve"
	"github.com/djangbahevans/goerp/internal/engine/user"
)

// engineVariables are the variables the engine injects into every
// template (notification-system.md §5 "Standard template variables").
var engineVariables = []TemplateVariable{
	{Name: "TenantName", Type: "string", Source: "engine"},
	{Name: "TenantLogoURL", Type: "string", Source: "engine"},
	{Name: "UserName", Type: "string", Source: "engine"},
	{Name: "UserFirstName", Type: "string", Source: "engine"},
	{Name: "ActionURL", Type: "string", Source: "engine"},
	{Name: "UnsubscribeURL", Type: "string", Source: "engine"},
	{Name: "Year", Type: "int", Source: "engine"},
}

// NotificationTemplateList is GET /admin/settings/notification-templates'
// response.
type NotificationTemplateList struct {
	Types []TemplateType `json:"types"`
}

type TemplateType struct {
	Type              string          `json:"type"`
	Module            string          `json:"module"`
	Label             string          `json:"label"`
	AvailableChannels []string        `json:"available_channels"`
	Templates         []TemplateEntry `json:"templates"`
}

// TemplateEntry is one (channel, locale) with a default or override row;
// Customised reports an override and HasDefault a shipped default.
type TemplateEntry struct {
	Channel    string `json:"channel"`
	Locale     string `json:"locale"`
	Customised bool   `json:"customised"`
	HasDefault bool   `json:"has_default"`
}

// NotificationTemplate is one template's GET, PUT and DELETE response.
// Default and Override are null when that row does not exist.
type NotificationTemplate struct {
	Type      string             `json:"type"`
	Channel   string             `json:"channel"`
	Locale    string             `json:"locale"`
	Default   map[string]string  `json:"default"`
	Override  map[string]string  `json:"override"`
	Variables []TemplateVariable `json:"variables"`
}

// TemplateVariable is a variable a template can use: a data_schema field
// (Source "data") or an engine-injected one (Source "engine").
type TemplateVariable struct {
	Name   string `json:"name"`
	Type   string `json:"type"`
	Source string `json:"source"`
}

type previewRequest struct {
	Template map[string]string `json:"template"`
	Data     map[string]any    `json:"data"`
}

// TemplatePreview is the preview route's response; SMS is set for the sms
// channel only.
type TemplatePreview struct {
	Rendered map[string]string `json:"rendered"`
	SMS      *SMSLength        `json:"sms,omitempty"`
}

type SMSLength struct {
	Characters int `json:"characters"`
	Segments   int `json:"segments"`
}

// templateTarget is the (type, channel, locale) a template route names.
type templateTarget struct {
	declared declaredType
	channel  string
	locale   string
}

func (t templateTarget) auditField() string {
	return "notification_templates." + t.declared.typ + "." + t.channel + "." + t.locale
}

// ServeListNotificationTemplates is GET
// /admin/settings/notification-templates.
func (h *Handler) ServeListNotificationTemplates(w http.ResponseWriter, r *http.Request) {
	c, ok := h.authorize(w, r)
	if !ok {
		return
	}
	variants, err := h.deps.Templates.TemplateVariants(r.Context(), c.tenant.Slug)
	if err != nil {
		writeInternalError(w, r, err, "list notification templates")
		return
	}
	byKey := map[string][]TemplateEntry{}
	for _, v := range variants {
		byKey[v.TemplateKey] = append(byKey[v.TemplateKey], TemplateEntry{Channel: v.Channel, Locale: v.Locale, Customised: v.HasOverride, HasDefault: v.HasDefault})
	}

	list := NotificationTemplateList{Types: []TemplateType{}}
	for _, d := range h.declaredTypes(c.tenant) {
		list.Types = append(list.Types, TemplateType{
			Type: d.typ, Module: d.module, Label: d.nt.Label,
			AvailableChannels: orEmpty(d.nt.AvailableChannels),
			Templates:         entriesOrEmpty(byKey[d.typ]),
		})
	}
	writeJSON(w, http.StatusOK, list)
}

// ServeGetNotificationTemplate is GET
// /admin/settings/notification-templates/{type}/{channel}/{locale}.
func (h *Handler) ServeGetNotificationTemplate(w http.ResponseWriter, r *http.Request) {
	c, ok := h.authorize(w, r)
	if !ok {
		return
	}
	t, ok := h.templateTarget(w, r, c.tenant)
	if !ok {
		return
	}
	h.writeTemplate(w, r, c, t)
}

// ServePutNotificationTemplate is PUT
// /admin/settings/notification-templates/{type}/{channel}/{locale}: it
// upserts the tenant's override once every column parses.
func (h *Handler) ServePutNotificationTemplate(w http.ResponseWriter, r *http.Request) {
	c, ok := h.authorize(w, r)
	if !ok {
		return
	}
	t, ok := h.templateTarget(w, r, c.tenant)
	if !ok {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	var body map[string]string
	if err := json.UnmarshalRead(r.Body, &body); err != nil {
		httperr.Write(r.Context(), w, http.StatusBadRequest, "invalid_request", "malformed request body")
		return
	}
	fields, ok := templateFields(w, r, t.channel, body)
	if !ok {
		return
	}

	row := notiftemplate.Row{TemplateKey: t.declared.typ, Channel: t.channel, Locale: t.locale, Fields: fields}
	if err := h.deps.Templates.SaveTemplateOverride(r.Context(), c.tenant.Slug, row); err != nil {
		writeInternalError(w, r, err, "save notification template")
		return
	}
	h.recordAudit(r, c, "tenant.settings_updated", map[string]any{"fields": []string{t.auditField()}})
	h.writeTemplate(w, r, c, t)
}

// ServeDeleteNotificationTemplate is DELETE
// /admin/settings/notification-templates/{type}/{channel}/{locale}: it
// deletes the tenant's override, if there is one.
func (h *Handler) ServeDeleteNotificationTemplate(w http.ResponseWriter, r *http.Request) {
	c, ok := h.authorize(w, r)
	if !ok {
		return
	}
	t, ok := h.templateTarget(w, r, c.tenant)
	if !ok {
		return
	}
	ctx := r.Context()
	_, override, err := h.deps.Templates.TemplateVersions(ctx, c.tenant.Slug, t.declared.typ, t.channel, t.locale)
	if err != nil {
		writeInternalError(w, r, err, "load notification template")
		return
	}
	if override != nil {
		if err := h.deps.Templates.ResetTemplate(ctx, c.tenant.Slug, t.declared.typ, t.channel, t.locale); err != nil {
			writeInternalError(w, r, err, "reset notification template")
			return
		}
		h.recordAudit(r, c, "tenant.settings_updated", map[string]any{"fields": []string{t.auditField()}})
	}
	h.writeTemplate(w, r, c, t)
}

// ServePreviewNotificationTemplate is POST
// /admin/settings/notification-templates/{type}/{channel}/{locale}/preview:
// it renders the request's template against sample data and writes
// nothing.
func (h *Handler) ServePreviewNotificationTemplate(w http.ResponseWriter, r *http.Request) {
	c, ok := h.authorize(w, r)
	if !ok {
		return
	}
	t, ok := h.templateTarget(w, r, c.tenant)
	if !ok {
		return
	}
	ctx := r.Context()
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	var body previewRequest
	if err := json.UnmarshalRead(r.Body, &body, json.RejectUnknownMembers(true)); err != nil {
		httperr.Write(ctx, w, http.StatusBadRequest, "invalid_request", "malformed request body")
		return
	}
	fields, ok := templateFields(w, r, t.channel, body.Template)
	if !ok {
		return
	}

	vars, base, err := h.previewVars(ctx, c, t.declared, body.Data)
	if err != nil {
		writeInternalError(w, r, err, "build preview data")
		return
	}
	preview := TemplatePreview{Rendered: map[string]string{}}
	for _, col := range notiftemplate.ChannelColumns[t.channel] {
		src, ok := fields[col]
		if !ok {
			continue
		}
		out, err := notiftemplate.RenderColumn(col, src, vars)
		if err != nil {
			writeTemplateError(w, r, col, err)
			return
		}
		preview.Rendered[col] = out
	}

	if html, ok := preview.Rendered[notiftemplate.ColHTML]; ok {
		wrapped, ferr, err := h.wrapInLayout(ctx, c.tenant, html, vars, base)
		if ferr != nil {
			writeFieldError(w, r, ferr)
			return
		}
		if err != nil {
			writeInternalError(w, r, err, "render email layout")
			return
		}
		preview.Rendered[notiftemplate.ColHTML] = wrapped
	}
	if sms, ok := preview.Rendered[notiftemplate.ColSMS]; ok {
		sms = strings.TrimSpace(sms)
		preview.Rendered[notiftemplate.ColSMS] = sms
		preview.SMS = smsLength(sms)
	}
	writeJSON(w, http.StatusOK, preview)
}

// templateTarget resolves the route's {type}, {channel} and {locale},
// writing a 404 for an undeclared type or unavailable channel and a 400
// for a malformed locale.
func (h *Handler) templateTarget(w http.ResponseWriter, r *http.Request, tc *tenantresolve.TenantContext) (templateTarget, bool) {
	params := route.ParamsFromContext(r.Context())
	typ, channel, locale := params["type"], params["channel"], params["locale"]
	types := h.declaredTypes(tc)
	i := slices.IndexFunc(types, func(d declaredType) bool { return d.typ == typ })
	if i < 0 {
		httperr.Write(r.Context(), w, http.StatusNotFound, "not_found", "unknown notification type")
		return templateTarget{}, false
	}
	declared := types[i]
	if !slices.Contains(declared.nt.AvailableChannels, channel) {
		httperr.Write(r.Context(), w, http.StatusNotFound, "not_found", "not one of this notification type's channels")
		return templateTarget{}, false
	}
	// Sends match locales exactly, so only the canonical casing a
	// recipient's locale has ("pt-BR", not "pt-br") can ever apply.
	if !l10n.ValidLocale(locale) {
		httperr.Write(r.Context(), w, http.StatusBadRequest, "invalid_request", "locale is not a BCP 47 tag in canonical form, such as en or pt-BR")
		return templateTarget{}, false
	}
	return templateTarget{declared: declared, channel: channel, locale: locale}, true
}

// templateFields validates body as channel's template content: only the
// channel's own columns, at least one of them non-empty, each one
// parsing. An empty column is left out, stored as NULL.
func templateFields(w http.ResponseWriter, r *http.Request, channel string, body map[string]string) (map[string]string, bool) {
	columns := notiftemplate.ChannelColumns[channel]
	fields := map[string]string{}
	for col, src := range body {
		if !slices.Contains(columns, col) {
			httperr.Write(r.Context(), w, http.StatusBadRequest, "invalid_request", fmt.Sprintf("%q is not a %s template column (%s)", col, channel, strings.Join(columns, ", ")))
			return nil, false
		}
		if src != "" {
			fields[col] = src
		}
	}
	if len(fields) == 0 {
		httperr.Write(r.Context(), w, http.StatusBadRequest, "invalid_request", "the template has no content")
		return nil, false
	}
	for _, col := range columns {
		if src, ok := fields[col]; ok {
			if err := notiftemplate.ParseColumn(col, src); err != nil {
				writeTemplateError(w, r, col, err)
				return nil, false
			}
		}
	}
	return fields, true
}

func writeTemplateError(w http.ResponseWriter, r *http.Request, col string, err error) {
	httperr.WriteDetails(r.Context(), w, http.StatusUnprocessableEntity, "invalid_template", "the template does not render", map[string]string{"field": col, "message": err.Error()})
}

func (h *Handler) writeTemplate(w http.ResponseWriter, r *http.Request, c caller, t templateTarget) {
	def, override, err := h.deps.Templates.TemplateVersions(r.Context(), c.tenant.Slug, t.declared.typ, t.channel, t.locale)
	if err != nil {
		writeInternalError(w, r, err, "load notification template")
		return
	}
	writeJSON(w, http.StatusOK, NotificationTemplate{
		Type: t.declared.typ, Channel: t.channel, Locale: t.locale,
		Default: def, Override: override,
		Variables: templateVariables(t.declared),
	}, json.FormatNilMapAsNull(true))
}

// templateVariables lists d's data_schema fields by name, then the
// engine-injected variables.
func templateVariables(d declaredType) []TemplateVariable {
	vars := []TemplateVariable{}
	for _, name := range slices.Sorted(maps.Keys(d.nt.DataSchema)) {
		vars = append(vars, TemplateVariable{Name: name, Type: d.nt.DataSchema[name], Source: "data"})
	}
	return append(vars, engineVariables...)
}

// previewVars is a preview's template data: a sample value per
// data_schema field, overridden by data, then the engine-injected
// variables for the tenant and the calling admin. It also returns the
// tenant's base URL.
func (h *Handler) previewVars(ctx context.Context, c caller, d declaredType, data map[string]any) (map[string]any, string, error) {
	vars := map[string]any{}
	for name, typ := range d.nt.DataSchema {
		switch typ {
		case "int":
			vars[name] = 1
		case "float":
			vars[name] = 1.5
		case "bool":
			vars[name] = true
		default:
			vars[name] = name
		}
	}
	maps.Copy(vars, data)

	profile, err := h.deps.TenantStore.GetProfile(ctx, c.tenant.TenantID)
	if err != nil {
		return nil, "", fmt.Errorf("load tenant profile: %w", err)
	}
	userName := ""
	switch p, err := h.deps.Users.GetProfile(ctx, c.auth.UserID); {
	case err == nil:
		userName = p.Name
	case !errors.Is(err, user.ErrProfileNotFound):
		return nil, "", fmt.Errorf("load admin profile: %w", err)
	}

	base := mailer.TenantBaseURL(h.deps.AppBaseURL, h.deps.PlatformDomain, c.tenant.Slug)
	logoURL := ""
	if profile.LogoURL != nil {
		logoURL = *profile.LogoURL
		if strings.HasPrefix(logoURL, "/") {
			logoURL = base + logoURL
		}
	}
	firstName, _, _ := strings.Cut(userName, " ")
	vars["TenantName"] = c.tenant.Name
	vars["TenantLogoURL"] = logoURL
	vars["UserName"] = userName
	vars["UserFirstName"] = firstName
	vars["ActionURL"] = base + "/"
	vars["UnsubscribeURL"] = base + "/_notif/unsubscribe?token=preview"
	vars["Year"] = time.Now().Year()
	return vars, base, nil
}

// wrapInLayout wraps html, a rendered email body, in the tenant's email
// layout (notification-system.md §10 "Email layout"). A layout that does
// not resolve or render is a *fieldError on email.layout_template.
func (h *Handler) wrapInLayout(ctx context.Context, tc *tenantresolve.TenantContext, html string, vars map[string]any, base string) (string, *fieldError, error) {
	cfg, err := h.deps.Notifications.Load(ctx, tc.TenantID)
	if err != nil {
		return "", nil, fmt.Errorf("load notification config: %w", err)
	}
	layout, err := notify.ResolveEmailLayout(h.snapshot(), cfg.Email.LayoutTemplate)
	if err != nil {
		return "", invalid("email.layout_template", "%s", err.Error()), nil
	}
	var buf bytes.Buffer
	if err := layout.Execute(&buf, map[string]any{
		"Content":        htmltemplate.HTML(html),
		"TenantName":     vars["TenantName"],
		"TenantLogoURL":  vars["TenantLogoURL"],
		"UnsubscribeURL": vars["UnsubscribeURL"],
		"Year":           vars["Year"],
	}); err != nil {
		return "", invalid("email.layout_template", "render email layout: %s", err.Error()), nil
	}
	return buf.String(), nil, nil
}

// gsmAlphabet is the GSM 03.38 default alphabet and its extension table.
const gsmAlphabet = "@£$¥èéùìòÇ\nØø\rÅåΔ_ΦΓΛΩΠΨΣΘΞÆæßÉ !\"#¤%&'()*+,-./0123456789:;<=>?¡ABCDEFGHIJKLMNOPQRSTUVWXYZÄÖÑÜ§¿abcdefghijklmnopqrstuvwxyzäöñüà" +
	"\f^{}\\[~]|€"

// smsLength counts text's characters in code points and the SMS segments
// it takes: 160 characters in one segment and 153 per segment beyond, or
// 70 and 67 when a character is outside the GSM 03.38 alphabet.
func smsLength(text string) *SMSLength {
	n := utf8.RuneCountInString(text)
	single, multi := 160, 153
	if strings.ContainsFunc(text, func(r rune) bool { return !strings.ContainsRune(gsmAlphabet, r) }) {
		single, multi = 70, 67
	}
	segments := 1
	if n > single {
		segments = (n + multi - 1) / multi
	}
	return &SMSLength{Characters: n, Segments: segments}
}

func entriesOrEmpty(entries []TemplateEntry) []TemplateEntry {
	if entries == nil {
		return []TemplateEntry{}
	}
	return entries
}
