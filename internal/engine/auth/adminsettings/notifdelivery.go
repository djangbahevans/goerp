package adminsettings

import (
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"maps"
	"math"
	"net/http"
	"net/mail"
	"net/netip"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/djangbahevans/goerp/internal/engine/emailprovider"
	"github.com/djangbahevans/goerp/internal/engine/httperr"
	"github.com/djangbahevans/goerp/internal/engine/manifest"
	"github.com/djangbahevans/goerp/internal/engine/module"
	"github.com/djangbahevans/goerp/internal/engine/notifconfig"
	"github.com/djangbahevans/goerp/internal/engine/notify"
	"github.com/djangbahevans/goerp/internal/engine/registry"
	tenantresolve "github.com/djangbahevans/goerp/internal/engine/tenant/resolve"
)

// secretMask is what GET returns for a stored secret, and what a PATCH or
// test email sends back to mean "the stored one".
const secretMask = "***"

const (
	maxFromNameLength = 200
	maxAddressBytes   = 254

	testEmailLimit  = 5
	testEmailWindow = 10 * time.Minute
	// testEmailTimeout bounds the synchronous send: without a deadline an
	// SMTP server that accepts the connection and never answers would
	// hold the request open indefinitely.
	testEmailTimeout = 30 * time.Second
)

var (
	hostLabelPattern     = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?$`)
	alphanumericSenderID = regexp.MustCompile(`^[A-Za-z0-9]{1,11}$`)
	e164Pattern          = regexp.MustCompile(`^\+[1-9][0-9]{1,14}$`)
)

// TestEmailSender is satisfied by *notify.EmailTester.
type TestEmailSender interface {
	Send(ctx context.Context, tenantID string, cfg notifconfig.EmailConfig, to string) (provider string, err error)
}

// NotificationDelivery is GET/PATCH /admin/settings/notification-delivery's
// response.
type NotificationDelivery struct {
	Channels DeliveryChannels    `json:"channels"`
	Email    DeliveryEmail       `json:"email"`
	SMS      DeliverySMS         `json:"sms"`
	Defaults map[string][]string `json:"defaults"`
	Types    []DeliveryType      `json:"types"`
	// Locked names every field an operator override fixes for the tenant.
	Locked []string `json:"locked"`
}

type DeliveryChannels struct {
	EmailEnabled bool `json:"email_enabled"`
	SMSEnabled   bool `json:"sms_enabled"`
	PushEnabled  bool `json:"push_enabled"`
}

// DeliveryEmail's APIKey reads secretMask when one is stored.
type DeliveryEmail struct {
	Provider       string       `json:"provider"`
	APIKey         string       `json:"api_key"`
	FromName       string       `json:"from_name"`
	FromAddr       string       `json:"from_addr"`
	ReplyTo        string       `json:"reply_to"`
	LayoutTemplate string       `json:"layout_template"`
	SMTP           DeliverySMTP `json:"smtp"`
}

// DeliverySMTP's Password reads secretMask when one is stored.
type DeliverySMTP struct {
	Host     string `json:"host"`
	Port     int    `json:"port"`
	User     string `json:"user"`
	Password string `json:"password"`
	UseTLS   bool   `json:"use_tls"`
}

type DeliverySMS struct {
	SenderID string `json:"sender_id"`
}

// DeliveryType is a notification type the tenant can set default
// channels for, with its manifest's own defaults.
type DeliveryType struct {
	Type              string   `json:"type"`
	Module            string   `json:"module"`
	Label             string   `json:"label"`
	DefaultChannels   []string `json:"default_channels"`
	AvailableChannels []string `json:"available_channels"`
}

// deliveryPatch mirrors NotificationDelivery without Types and Locked,
// every field optional. A null defaults entry returns that type to its
// manifest defaults.
type deliveryPatch struct {
	Channels *channelsPatch       `json:"channels"`
	Email    *emailPatch          `json:"email"`
	SMS      *smsPatch            `json:"sms"`
	Defaults map[string]*[]string `json:"defaults"`
}

type channelsPatch struct {
	EmailEnabled *bool `json:"email_enabled"`
	SMSEnabled   *bool `json:"sms_enabled"`
	PushEnabled  *bool `json:"push_enabled"`
}

type emailPatch struct {
	Provider       *string    `json:"provider"`
	APIKey         *string    `json:"api_key"`
	FromName       *string    `json:"from_name"`
	FromAddr       *string    `json:"from_addr"`
	ReplyTo        *string    `json:"reply_to"`
	LayoutTemplate *string    `json:"layout_template"`
	SMTP           *smtpPatch `json:"smtp"`
}

type smtpPatch struct {
	Host     *string `json:"host"`
	Port     *int    `json:"port"`
	User     *string `json:"user"`
	Password *string `json:"password"`
	UseTLS   *bool   `json:"use_tls"`
}

type smsPatch struct {
	SenderID *string `json:"sender_id"`
}

type testEmailRequest struct {
	Email *emailPatch `json:"email"`
}

// TestEmailResult is POST .../test-email's response.
type TestEmailResult struct {
	SentTo   string `json:"sent_to"`
	Provider string `json:"provider"`
}

// deliveryField maps a response field to its notifconfig key and value.
type deliveryField struct {
	name  string
	key   string
	value func(*notifconfig.Config) any
}

// deliveryFields is every scalar field, in the order a PATCH reports
// the first locked one. defaults is handled per type.
var deliveryFields = []deliveryField{
	{"channels.email_enabled", notifconfig.KeyEmailEnabled, func(c *notifconfig.Config) any { return c.EmailEnabled }},
	{"channels.sms_enabled", notifconfig.KeySMSEnabled, func(c *notifconfig.Config) any { return c.SMSEnabled }},
	{"channels.push_enabled", notifconfig.KeyPushEnabled, func(c *notifconfig.Config) any { return c.PushEnabled }},
	{"email.provider", notifconfig.KeyEmailProvider, func(c *notifconfig.Config) any { return c.Email.Provider }},
	{"email.api_key", notifconfig.KeyEmailAPIKey, func(c *notifconfig.Config) any { return c.Email.APIKey }},
	{"email.from_name", notifconfig.KeyEmailFromName, func(c *notifconfig.Config) any { return c.Email.FromName }},
	{"email.from_addr", notifconfig.KeyEmailFromAddr, func(c *notifconfig.Config) any { return c.Email.FromAddr }},
	{"email.reply_to", notifconfig.KeyEmailReplyTo, func(c *notifconfig.Config) any { return c.Email.ReplyTo }},
	{"email.layout_template", notifconfig.KeyEmailLayout, func(c *notifconfig.Config) any { return c.Email.LayoutTemplate }},
	{"email.smtp.host", notifconfig.KeySMTPHost, func(c *notifconfig.Config) any { return c.Email.SMTP.Host }},
	{"email.smtp.port", notifconfig.KeySMTPPort, func(c *notifconfig.Config) any { return c.Email.SMTP.Port }},
	{"email.smtp.user", notifconfig.KeySMTPUser, func(c *notifconfig.Config) any { return c.Email.SMTP.User }},
	{"email.smtp.password", notifconfig.KeySMTPPassword, func(c *notifconfig.Config) any { return c.Email.SMTP.Password }},
	{"email.smtp.use_tls", notifconfig.KeySMTPUseTLS, func(c *notifconfig.Config) any { return c.Email.SMTP.UseTLS }},
	{"sms.sender_id", notifconfig.KeySMSSenderID, func(c *notifconfig.Config) any { return c.SMS.SenderID }},
}

const defaultsField = "defaults"

// ServeGetNotificationDelivery is GET /admin/settings/notification-delivery.
func (h *Handler) ServeGetNotificationDelivery(w http.ResponseWriter, r *http.Request) {
	c, ok := h.authorize(w, r)
	if !ok {
		return
	}
	cfg, locked, err := h.loadDelivery(r.Context(), c.tenant.TenantID)
	if err != nil {
		writeInternalError(w, r, err, "load notification delivery settings")
		return
	}
	writeJSON(w, http.StatusOK, h.deliveryResponse(c.tenant, cfg, locked))
}

// ServePatchNotificationDelivery is PATCH
// /admin/settings/notification-delivery: every field present is
// validated, then every changed one is written in one transaction.
func (h *Handler) ServePatchNotificationDelivery(w http.ResponseWriter, r *http.Request) {
	c, ok := h.authorize(w, r)
	if !ok {
		return
	}
	ctx := r.Context()

	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	var body deliveryPatch
	if err := json.UnmarshalRead(r.Body, &body, json.RejectUnknownMembers(true)); err != nil {
		httperr.Write(ctx, w, http.StatusBadRequest, "invalid_request", "malformed request body")
		return
	}

	current, locked, err := h.loadDelivery(ctx, c.tenant.TenantID)
	if err != nil {
		writeInternalError(w, r, err, "load notification delivery settings")
		return
	}
	next, ferr := h.applyDeliveryPatch(c.tenant, current, body)
	if ferr != nil {
		writeFieldError(w, r, ferr)
		return
	}
	writes, changed, lockedField := diffDelivery(current, next, locked)
	if lockedField != "" {
		httperr.WriteDetails(ctx, w, http.StatusUnprocessableEntity, "locked_setting", "this setting is set by your platform operator", map[string]string{"field": lockedField})
		return
	}

	if len(writes) > 0 {
		if err := h.deps.Notifications.SetMany(ctx, c.tenant.TenantID, c.tenant.Slug, writes, c.auth.UserID); err != nil {
			writeInternalError(w, r, err, "save notification delivery settings")
			return
		}
		h.recordAudit(r, c, "tenant.settings_updated", map[string]any{"fields": changed})
	}

	updated, locked, err := h.loadDelivery(ctx, c.tenant.TenantID)
	if err != nil {
		writeInternalError(w, r, err, "reload notification delivery settings")
		return
	}
	writeJSON(w, http.StatusOK, h.deliveryResponse(c.tenant, updated, locked))
}

// ServeTestEmail is POST /admin/settings/notification-delivery/test-email:
// it sends a test email to the calling admin with the request's email
// settings over the saved ones, and writes nothing.
func (h *Handler) ServeTestEmail(w http.ResponseWriter, r *http.Request) {
	c, ok := h.authorize(w, r)
	if !ok {
		return
	}
	ctx := r.Context()

	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	var body testEmailRequest
	if err := json.UnmarshalRead(r.Body, &body, json.RejectUnknownMembers(true)); err != nil {
		httperr.Write(ctx, w, http.StatusBadRequest, "invalid_request", "malformed request body")
		return
	}

	cfg, err := h.deps.Notifications.Load(ctx, c.tenant.TenantID)
	if err != nil {
		writeInternalError(w, r, err, "load notification delivery settings")
		return
	}
	email := cfg.Email
	if body.Email != nil {
		if ferr := h.applyEmailPatch(&email, body.Email); ferr != nil {
			writeFieldError(w, r, ferr)
			return
		}
	}
	if ferr := h.sendable(email); ferr != nil {
		writeFieldError(w, r, ferr)
		return
	}

	admin, err := h.deps.Users.GetByID(ctx, c.auth.UserID)
	if err != nil {
		writeInternalError(w, r, err, "load admin user")
		return
	}

	if h.deps.Cache != nil {
		key := "adminsettings:test_email:" + c.tenant.TenantID + ":" + c.auth.UserID
		allowed, retryAfter, err := h.deps.Cache.SlidingWindowAllow(ctx, key, testEmailLimit, testEmailWindow)
		if err != nil {
			writeInternalError(w, r, err, "test email rate limit")
			return
		}
		if !allowed {
			w.Header().Set("Retry-After", strconv.Itoa(max(1, int(math.Ceil(retryAfter.Seconds())))))
			httperr.Write(ctx, w, http.StatusTooManyRequests, "rate_limit_exceeded", fmt.Sprintf("at most %d test emails per %d minutes", testEmailLimit, int(testEmailWindow.Minutes())))
			return
		}
	}

	sendCtx, cancel := context.WithTimeout(ctx, testEmailTimeout)
	defer cancel()
	provider, err := h.deps.TestEmail.Send(sendCtx, c.tenant.TenantID, email, admin.Email)
	if cerr, ok := errors.AsType[*notify.ConfigError](err); ok {
		writeFieldError(w, r, invalid(cerr.Field, "%s", cerr.Err.Error()))
		return
	}
	if err != nil {
		message := err.Error()
		if errors.Is(err, emailprovider.ErrConnection) {
			message = "could not connect to the email provider"
		}

		httperr.WriteDetails(ctx, w, http.StatusBadGateway, "test_email_failed", "the email provider did not accept the test email", map[string]string{"message": message})
		return
	}
	writeJSON(w, http.StatusOK, TestEmailResult{SentTo: admin.Email, Provider: provider})
}

func (h *Handler) loadDelivery(ctx context.Context, tenantID string) (*notifconfig.Config, []string, error) {
	cfg, err := h.deps.Notifications.Load(ctx, tenantID)
	if err != nil {
		return nil, nil, fmt.Errorf("load notification config: %w", err)
	}
	locked, err := h.deps.Notifications.Locked(ctx, tenantID)
	if err != nil {
		return nil, nil, fmt.Errorf("load notification config overrides: %w", err)
	}
	return cfg, locked, nil
}

func (h *Handler) deliveryResponse(tc *tenantresolve.TenantContext, cfg *notifconfig.Config, lockedKeys []string) *NotificationDelivery {
	locked := []string{}
	for _, f := range deliveryFields {
		if slices.Contains(lockedKeys, f.key) {
			locked = append(locked, f.name)
		}
	}
	if slices.Contains(lockedKeys, notifconfig.KeyDefaults) {
		locked = append(locked, defaultsField)
	}

	defaults := cfg.Defaults
	if defaults == nil {
		defaults = map[string][]string{}
	}
	return &NotificationDelivery{
		Channels: DeliveryChannels{EmailEnabled: cfg.EmailEnabled, SMSEnabled: cfg.SMSEnabled, PushEnabled: cfg.PushEnabled},
		Email: DeliveryEmail{
			Provider:       cfg.Email.Provider,
			APIKey:         maskSecret(cfg.Email.APIKey),
			FromName:       cfg.Email.FromName,
			FromAddr:       cfg.Email.FromAddr,
			ReplyTo:        cfg.Email.ReplyTo,
			LayoutTemplate: cfg.Email.LayoutTemplate,
			SMTP: DeliverySMTP{
				Host:     cfg.Email.SMTP.Host,
				Port:     cfg.Email.SMTP.Port,
				User:     cfg.Email.SMTP.User,
				Password: maskSecret(cfg.Email.SMTP.Password),
				UseTLS:   cfg.Email.SMTP.UseTLS,
			},
		},
		SMS:      DeliverySMS{SenderID: cfg.SMS.SenderID},
		Defaults: defaults,
		Types:    h.deliveryTypes(tc),
		Locked:   locked,
	}
}

func maskSecret(s string) string {
	if s == "" {
		return ""
	}
	return secretMask
}

// declaredType is a notification type as the engine or a module declares
// it, under its full "{module}.{name}" type string.
type declaredType struct {
	typ    string
	module string
	nt     manifest.NotificationType
}

// declaredTypes lists the engine's notification types and those of every
// module ready and enabled for the tenant, ordered by type.
func (h *Handler) declaredTypes(tc *tenantresolve.TenantContext) []declaredType {
	var types []declaredType
	add := func(moduleName string, declared []manifest.NotificationType) {
		for _, nt := range declared {
			types = append(types, declaredType{typ: moduleName + "." + nt.Name, module: moduleName, nt: nt})
		}
	}
	add(notify.EngineModule, notify.EngineTypes)
	if snapshot := h.snapshot(); snapshot != nil {
		for name, m := range snapshot.Modules() {
			if m.Status == module.StatusReady && tc.Entitlements.ModuleEnabled(name) {
				add(name, m.Manifest.NotificationTypes)
			}
		}
	}
	slices.SortFunc(types, func(a, b declaredType) int { return strings.Compare(a.typ, b.typ) })
	return types
}

// deliveryTypes is declaredTypes as GET
// /admin/settings/notification-delivery lists them.
func (h *Handler) deliveryTypes(tc *tenantresolve.TenantContext) []DeliveryType {
	types := []DeliveryType{}
	for _, d := range h.declaredTypes(tc) {
		types = append(types, DeliveryType{
			Type:              d.typ,
			Module:            d.module,
			Label:             d.nt.Label,
			DefaultChannels:   orEmpty(d.nt.DefaultChannels),
			AvailableChannels: orEmpty(d.nt.AvailableChannels),
		})
	}
	return types
}

func (h *Handler) snapshot() *registry.RegistrySnapshot {
	if h.deps.Registry == nil {
		return nil
	}
	return h.deps.Registry.Snapshot()
}

// applyDeliveryPatch validates body and returns current with it applied.
func (h *Handler) applyDeliveryPatch(tc *tenantresolve.TenantContext, current *notifconfig.Config, body deliveryPatch) (*notifconfig.Config, *fieldError) {
	next := *current
	next.Defaults = maps.Clone(current.Defaults)
	if next.Defaults == nil {
		next.Defaults = map[string][]string{}
	}

	if ch := body.Channels; ch != nil {
		setBool(&next.EmailEnabled, ch.EmailEnabled)
		setBool(&next.SMSEnabled, ch.SMSEnabled)
		setBool(&next.PushEnabled, ch.PushEnabled)
	}
	if body.Email != nil {
		if ferr := h.applyEmailPatch(&next.Email, body.Email); ferr != nil {
			return nil, ferr
		}
	}
	if body.SMS != nil && body.SMS.SenderID != nil {
		id := strings.TrimSpace(*body.SMS.SenderID)
		if id != "" && !alphanumericSenderID.MatchString(id) && !e164Pattern.MatchString(id) {
			return nil, invalid("sms.sender_id", "a sender ID is 1 to 11 letters and digits, or a phone number in E.164 form such as +233201234567")
		}
		next.SMS.SenderID = id
	}

	if len(body.Defaults) > 0 {
		types := h.deliveryTypes(tc)
		for _, typ := range slices.Sorted(maps.Keys(body.Defaults)) {
			channels := body.Defaults[typ]
			field := defaultsField + "." + typ
			if channels == nil {
				delete(next.Defaults, typ)
				continue
			}
			i := slices.IndexFunc(types, func(t DeliveryType) bool { return t.Type == typ })
			if i < 0 {
				return nil, invalid(field, "%q is not a notification type of an installed module", typ)
			}
			picked := []string{}
			for _, ch := range *channels {
				if !slices.Contains(types[i].AvailableChannels, ch) {
					return nil, invalid(field, "%q is not one of this type's channels (%s)", ch, strings.Join(types[i].AvailableChannels, ", "))
				}
				if !slices.Contains(picked, ch) {
					picked = append(picked, ch)
				}
			}
			next.Defaults[typ] = picked
		}
	}
	return &next, nil
}

// applyEmailPatch validates p and applies it to cfg. A secret sent as
// secretMask keeps cfg's.
func (h *Handler) applyEmailPatch(cfg *notifconfig.EmailConfig, p *emailPatch) *fieldError {
	if p.Provider != nil {
		if *p.Provider != notifconfig.ProviderResend && *p.Provider != notifconfig.ProviderSMTP {
			return invalid("email.provider", "provider is one of %s, %s", notifconfig.ProviderResend, notifconfig.ProviderSMTP)
		}
		cfg.Provider = *p.Provider
	}
	setSecret(&cfg.APIKey, p.APIKey)
	if p.FromName != nil {
		name := strings.TrimSpace(*p.FromName)
		if utf8.RuneCountInString(name) > maxFromNameLength {
			return invalid("email.from_name", "a sender name is at most %d characters", maxFromNameLength)
		}
		cfg.FromName = name
	}
	if p.FromAddr != nil {
		addr := strings.TrimSpace(*p.FromAddr)
		if !validEmailAddress(addr) {
			return invalid("email.from_addr", "the From address is an email address, such as noreply@example.com")
		}
		cfg.FromAddr = addr
	}
	if p.ReplyTo != nil {
		addr := strings.TrimSpace(*p.ReplyTo)
		if addr != "" && !validEmailAddress(addr) {
			return invalid("email.reply_to", "the reply-to address is an email address, such as support@example.com")
		}
		cfg.ReplyTo = addr
	}
	if p.LayoutTemplate != nil {
		ref := strings.TrimSpace(*p.LayoutTemplate)
		if ref != "" {
			if _, err := notify.ResolveEmailLayout(h.snapshot(), ref); err != nil {
				return invalid("email.layout_template", "%s", err.Error())
			}
		}
		cfg.LayoutTemplate = ref
	}
	if s := p.SMTP; s != nil {
		if s.Host != nil {
			host := strings.TrimSpace(*s.Host)
			if host != "" && !validHost(host) {
				return invalid("email.smtp.host", "the SMTP host is a hostname or IP address, such as smtp.example.com")
			}
			cfg.SMTP.Host = host
		}
		if s.Port != nil {
			if *s.Port < 1 || *s.Port > 65535 {
				return invalid("email.smtp.port", "the SMTP port is 1 to 65535")
			}
			cfg.SMTP.Port = *s.Port
		}
		if s.User != nil {
			cfg.SMTP.User = strings.TrimSpace(*s.User)
		}
		setSecret(&cfg.SMTP.Password, s.Password)
		setBool(&cfg.SMTP.UseTLS, s.UseTLS)
	}
	return nil
}

// sendable reports the first setting missing for cfg's provider to send.
func (h *Handler) sendable(cfg notifconfig.EmailConfig) *fieldError {
	if cfg.FromAddr == "" {
		return invalid("email.from_addr", "a From address is required to send email")
	}
	switch cfg.Provider {
	case notifconfig.ProviderResend:
		if cfg.APIKey == "" {
			return invalid("email.api_key", "an API key is required to send email through Resend")
		}
	case notifconfig.ProviderSMTP:
		if cfg.SMTP.Host == "" {
			return invalid("email.smtp.host", "an SMTP host is required to send email through SMTP")
		}
	}
	if _, err := notify.ResolveEmailLayout(h.snapshot(), cfg.LayoutTemplate); err != nil {
		return invalid("email.layout_template", "%s", err.Error())
	}
	return nil
}

// diffDelivery returns the notifconfig writes that turn current into next
// and the fields they change. A cleared string is written as nil, which
// removes the tenant's value. lockedField names the first changed field an
// operator override fixes, in which case nothing is to be written.
func diffDelivery(current, next *notifconfig.Config, lockedKeys []string) (writes map[string]any, changed []string, lockedField string) {
	writes = map[string]any{}
	for _, f := range deliveryFields {
		value := f.value(next)
		if value == f.value(current) {
			continue
		}
		if slices.Contains(lockedKeys, f.key) {
			return nil, nil, f.name
		}
		if s, ok := value.(string); ok && s == "" {
			value = nil
		}
		writes[f.key] = value
		changed = append(changed, f.name)
	}

	var defaultsChanged []string
	for _, typ := range slices.Sorted(maps.Keys(current.Defaults)) {
		if _, ok := next.Defaults[typ]; !ok {
			defaultsChanged = append(defaultsChanged, defaultsField+"."+typ)
		}
	}
	for _, typ := range slices.Sorted(maps.Keys(next.Defaults)) {
		if was, ok := current.Defaults[typ]; !ok || !slices.Equal(was, next.Defaults[typ]) {
			defaultsChanged = append(defaultsChanged, defaultsField+"."+typ)
		}
	}
	if len(defaultsChanged) > 0 {
		if slices.Contains(lockedKeys, notifconfig.KeyDefaults) {
			return nil, nil, defaultsField
		}
		slices.Sort(defaultsChanged)
		changed = append(changed, defaultsChanged...)
		if len(next.Defaults) == 0 {
			writes[notifconfig.KeyDefaults] = nil
		} else {
			writes[notifconfig.KeyDefaults] = next.Defaults
		}
	}
	return writes, changed, ""
}

func orEmpty(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func setBool(dst *bool, v *bool) {
	if v != nil {
		*dst = *v
	}
}

func setSecret(dst *string, v *string) {
	if v != nil && *v != secretMask {
		*dst = *v
	}
}

func validEmailAddress(s string) bool {
	if s == "" || len(s) > maxAddressBytes {
		return false
	}
	addr, err := mail.ParseAddress(s)
	return err == nil && addr.Name == "" && addr.Address == s
}

func validHost(s string) bool {
	if _, err := netip.ParseAddr(s); err == nil {
		return true
	}
	if len(s) > 253 {
		return false
	}
	for label := range strings.SplitSeq(strings.TrimSuffix(s, "."), ".") {
		if !hostLabelPattern.MatchString(label) {
			return false
		}
	}
	return true
}
