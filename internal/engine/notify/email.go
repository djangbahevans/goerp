package notify

import (
	"bytes"
	"cmp"
	"context"
	"database/sql"
	_ "embed"
	"encoding/json/v2"
	"errors"
	"fmt"
	htmltemplate "html/template"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/emailprovider"
	"github.com/djangbahevans/goerp/internal/engine/jobqueue"
	"github.com/djangbahevans/goerp/internal/engine/mailer"
	"github.com/djangbahevans/goerp/internal/engine/notifconfig"
	"github.com/djangbahevans/goerp/internal/engine/notifications"
	"github.com/djangbahevans/goerp/internal/engine/notiftemplate"
	"github.com/djangbahevans/goerp/internal/engine/tenant"
	"github.com/djangbahevans/goerp/internal/engine/tenantschema"
	"github.com/riverqueue/river"
	"github.com/rs/zerolog/log"
)

//go:embed layouts/default.html
var defaultLayoutSource string

var defaultLayout = htmltemplate.Must(htmltemplate.New("layout").Parse(defaultLayoutSource))

// defaultEmailBody is the HTML body of a notification whose type has no
// email template: its in-app title, body and action link.
var defaultEmailBody = htmltemplate.Must(htmltemplate.New("body").Parse(
	`<p><strong>{{.Title}}</strong></p>{{if .Body}}<p>{{.Body}}</p>{{end}}{{if .ActionURL}}<p><a href="{{.ActionURL}}">View in {{.TenantName}}</a></p>{{end}}`))

// errEmailPermanent marks a delivery that no retry can fix: the send is
// failed at once rather than after every attempt.
var errEmailPermanent = errors.New("email delivery failed permanently")

// EmailTenants loads a tenant and its profile — satisfied by
// *tenant.Store.
type EmailTenants interface {
	GetByID(ctx context.Context, id string) (*tenant.Tenant, error)
	GetProfile(ctx context.Context, tenantID string) (*tenant.Profile, error)
}

// UnsubscribeIssuer mints unsubscribe tokens — satisfied by
// *notifications.UnsubscribeCodec.
type UnsubscribeIssuer interface {
	IssueAt(userID, tenantID, notificationType string, now time.Time) (string, error)
}

// EmailDeps are an EmailWorker's collaborators. AppBaseURL and
// PlatformDomain build the tenant's links the same way invite emails do
// (mailer.TenantBaseURL). ResendBaseURL defaults to Resend's production
// API.
type EmailDeps struct {
	DB             *sql.DB
	Registry       Registry
	Config         ConfigLoader
	Tenants        EmailTenants
	Unsubscribe    UnsubscribeIssuer
	AppBaseURL     string
	PlatformDomain string
	ResendBaseURL  string
	HTTPClient     *http.Client
}

// EmailWorker works email_send (notification-system.md §4, §10): it
// renders one email delivery, sends it through the tenant's configured
// provider, and records the outcome on its notification_deliveries row.
type EmailWorker struct {
	river.WorkerDefaults[jobqueue.EmailSendArgs]
	EmailDeps
}

func NewEmailWorker(d EmailDeps) *EmailWorker {
	return &EmailWorker{EmailDeps: d}
}

// emailDelivery is an email_send job's delivery row and its notification.
type emailDelivery struct {
	status    string
	userID    string
	typ       string
	module    string
	title     string
	body      string
	actionURL string
	data      map[string]any
	createdAt time.Time
}

// Work sends args' delivery unless it already reached a final status: a
// job retried after its send succeeded and its row was updated sends
// nothing. A send that fails transiently leaves the row retrying (or
// quota_exceeded) for River to retry; its last attempt, or a permanent
// failure, leaves it failed.
func (w *EmailWorker) Work(ctx context.Context, job *river.Job[jobqueue.EmailSendArgs]) error {
	args := job.Args
	d, err := w.loadDelivery(ctx, args.TenantSlug, args.DeliveryID)
	if errors.Is(err, sql.ErrNoRows) {
		log.Info().Str("delivery_id", args.DeliveryID).Msg("email_send: delivery no longer exists")
		return nil
	}
	if err != nil {
		return err
	}
	switch d.status {
	case notifications.DeliveryDelivered, notifications.DeliveryAccepted, notifications.DeliveryFailed, notifications.DeliverySkipped:
		log.Info().Str("delivery_id", args.DeliveryID).Str("status", d.status).Msg("email_send: delivery already final, not resending")
		return nil
	}

	started := time.Now()
	sender, msg, err := w.prepare(ctx, args, d)
	if err != nil {
		provider := ""
		if sender != nil {
			provider = sender.Name()
		}
		return w.fail(ctx, job, provider, err)
	}

	providerID, err := sender.Send(ctx, msg)
	if errors.Is(err, emailprovider.ErrAlreadySent) {
		log.Warn().Err(err).Str("delivery_id", args.DeliveryID).Msg("email_send: an earlier attempt already sent this email")
		err = nil
	}
	if err != nil {
		return w.fail(ctx, job, sender.Name(), err)
	}
	if err := w.update(ctx, args, notifications.DeliveryDelivered, sender.Name(), providerID, job.Attempt, ""); err != nil {
		return err
	}
	log.Info().
		Str("tenant_id", args.TenantID).
		Str("user_id", d.userID).
		Str("type", d.typ).
		Str("channel", notifications.ChannelEmail).
		Str("provider", sender.Name()).
		Str("provider_id", providerID).
		Int64("duration_ms", time.Since(started).Milliseconds()).
		Msg("notification delivered")
	return nil
}

// NextRetry backs off exponentially: 30s, 1m, 2m, 4m between the five
// attempts.
func (w *EmailWorker) NextRetry(job *river.Job[jobqueue.EmailSendArgs]) time.Time {
	return time.Now().Add(30 * time.Second << max(job.Attempt-1, 0))
}

// fail records a failed attempt. A permanent failure or the last attempt
// leaves the row failed and schedules nothing more; any other leaves it
// retrying, or quota_exceeded for a provider quota refusal, and returns
// cause for River to retry.
func (w *EmailWorker) fail(ctx context.Context, job *river.Job[jobqueue.EmailSendArgs], provider string, cause error) error {
	permanent := errors.Is(cause, errEmailPermanent) || errors.Is(cause, emailprovider.ErrPermanent)
	status := notifications.DeliveryRetrying
	switch {
	case permanent || job.Attempt >= job.MaxAttempts:
		status = notifications.DeliveryFailed
	case errors.Is(cause, emailprovider.ErrQuotaExceeded):
		status = notifications.DeliveryQuotaExceeded
	}
	if err := w.update(ctx, job.Args, status, provider, "", job.Attempt, cause.Error()); err != nil {
		return errors.Join(cause, err)
	}
	log.Warn().Err(cause).
		Str("tenant_id", job.Args.TenantID).
		Str("delivery_id", job.Args.DeliveryID).
		Str("status", status).
		Int("attempt", job.Attempt).
		Msg("email_send: delivery attempt failed")
	if status == notifications.DeliveryFailed {
		return river.JobCancel(cause)
	}
	return cause
}

func (w *EmailWorker) loadDelivery(ctx context.Context, tenantSlug, deliveryID string) (*emailDelivery, error) {
	schema := tenantschema.Name(tenantSlug)
	var d emailDelivery
	var data []byte
	err := w.DB.QueryRowContext(ctx, fmt.Sprintf(`
		SELECT d.status, n.user_id, n.type, n.module, n.title, COALESCE(n.body, ''), COALESCE(n.action_url, ''), n.data, n.created_at
		FROM %s.notification_deliveries d
		JOIN %s.notifications n ON n.id = d.notification_id
		WHERE d.id = $1
	`, schema, schema), deliveryID).Scan(&d.status, &d.userID, &d.typ, &d.module, &d.title, &d.body, &d.actionURL, &data, &d.createdAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
		return nil, fmt.Errorf("load email delivery: %w", err)
	}
	if len(data) > 0 {
		if err := json.Unmarshal(data, &d.data); err != nil {
			return nil, fmt.Errorf("decode notification data: %w", err)
		}
	}
	return &d, nil
}

// update records an attempt's outcome, the provider that handled it, and
// its provider ID once delivered.
func (w *EmailWorker) update(ctx context.Context, args jobqueue.EmailSendArgs, status, provider, providerID string, attempt int, reason string) error {
	_, err := w.DB.ExecContext(ctx, fmt.Sprintf(`
		UPDATE %s.notification_deliveries SET
			status         = $2,
			provider       = COALESCE(NULLIF($3, ''), provider),
			provider_id    = NULLIF($4, ''),
			attempted_at   = NOW(),
			delivered_at   = CASE WHEN $2 = 'delivered' THEN NOW() END,
			attempt_count  = $5,
			failure_reason = NULLIF($6, '')
		WHERE id = $1
	`, tenantschema.Name(args.TenantSlug)), args.DeliveryID, status, provider, providerID, attempt, reason)
	if err != nil {
		return fmt.Errorf("update email delivery: %w", err)
	}
	return nil
}

// prepare picks the tenant's provider and renders the message. Everything
// that feeds the message is fixed by the notification, not the attempt —
// the unsubscribe token and {{.Year}} are as of the notification's
// creation — so a retry sends the same message under the same
// idempotency key.
func (w *EmailWorker) prepare(ctx context.Context, args jobqueue.EmailSendArgs, d *emailDelivery) (emailprovider.Sender, emailprovider.Message, error) {
	var msg emailprovider.Message
	t, err := w.Tenants.GetByID(ctx, args.TenantID)
	if err != nil {
		return nil, msg, fmt.Errorf("load tenant: %w", err)
	}
	cfg, err := w.Config.Load(ctx, args.TenantID)
	if err != nil {
		return nil, msg, fmt.Errorf("load notification config: %w", err)
	}
	sender, err := w.provider(cfg.Email)
	if err != nil {
		return nil, msg, err
	}
	profile, err := w.Tenants.GetProfile(ctx, args.TenantID)
	if err != nil {
		return sender, msg, fmt.Errorf("load tenant profile: %w", err)
	}
	user, err := w.loadUser(ctx, d.userID)
	if err != nil {
		return sender, msg, err
	}

	base := mailer.TenantBaseURL(w.AppBaseURL, w.PlatformDomain, t.Slug)
	token, err := w.Unsubscribe.IssueAt(d.userID, t.ID, d.typ, d.createdAt)
	if err != nil {
		return sender, msg, err
	}
	unsubscribeURL := base + "/_notif/unsubscribe?token=" + url.QueryEscape(token)
	logoURL := ""
	if profile.LogoURL != nil {
		logoURL = absoluteURL(base, *profile.LogoURL)
	}

	vars := templateVars(d.data, t, user, Options{})
	vars["Year"] = d.createdAt.Year()
	vars["TenantLogoURL"] = logoURL
	vars["UnsubscribeURL"] = unsubscribeURL
	if d.actionURL != "" {
		vars["ActionURL"] = absoluteURL(base, d.actionURL)
	}

	content, err := w.render(d, user.locale, vars)
	if err != nil {
		return sender, msg, fmt.Errorf("%w: %w", errEmailPermanent, err)
	}
	layout, err := w.layout(cfg.Email.LayoutTemplate)
	if err != nil {
		return sender, msg, fmt.Errorf("%w: %w", errEmailPermanent, err)
	}
	var html bytes.Buffer
	if err := layout.Execute(&html, map[string]any{
		"Content":        htmltemplate.HTML(content.html),
		"TenantName":     t.Name,
		"TenantLogoURL":  logoURL,
		"UnsubscribeURL": unsubscribeURL,
		"Year":           d.createdAt.Year(),
	}); err != nil {
		return sender, msg, fmt.Errorf("%w: render email layout: %w", errEmailPermanent, err)
	}

	text := content.text
	if !strings.Contains(text, unsubscribeURL) {
		text = strings.TrimRight(text, "\n") + "\n\n--\nUnsubscribe from these emails: " + unsubscribeURL + "\n"
	}

	msg = emailprovider.Message{
		FromName:  cmp.Or(cfg.Email.FromName, t.Name),
		FromAddr:  cfg.Email.FromAddr,
		ReplyTo:   cfg.Email.ReplyTo,
		To:        args.Recipient,
		Subject:   content.subject,
		HTML:      html.String(),
		Text:      text,
		MessageID: messageID(args.NotificationID, cfg.Email.FromAddr, w.PlatformDomain),
		Headers: []emailprovider.Header{
			{Name: "List-Unsubscribe", Value: "<" + unsubscribeURL + ">"},
			{Name: "List-Unsubscribe-Post", Value: "List-Unsubscribe=One-Click"},
			{Name: "X-GoERP-Tenant", Value: t.Slug},
			{Name: "X-GoERP-Type", Value: d.typ},
		},
		IdempotencyKey: args.IdempotencyKey,
	}
	return sender, msg, nil
}

// provider builds the adapter cfg selects, reading its credentials from
// cfg as it is now: a provider switch applies to the next send.
func (w *EmailWorker) provider(cfg notifconfig.EmailConfig) (emailprovider.Sender, error) {
	if cfg.FromAddr == "" {
		return nil, fmt.Errorf("%w: notifications.email.from_addr is not set", errEmailPermanent)
	}
	switch cfg.Provider {
	case notifconfig.ProviderResend:
		if cfg.APIKey == "" {
			return nil, fmt.Errorf("%w: notifications.email.api_key is not set", errEmailPermanent)
		}
		return &emailprovider.Resend{APIKey: cfg.APIKey, BaseURL: w.ResendBaseURL, Client: w.HTTPClient}, nil
	case notifconfig.ProviderSMTP:
		if cfg.SMTP.Host == "" {
			return nil, fmt.Errorf("%w: notifications.email.smtp.host is not set", errEmailPermanent)
		}
		return &emailprovider.SMTP{Host: cfg.SMTP.Host, Port: cfg.SMTP.Port, User: cfg.SMTP.User, Password: cfg.SMTP.Password, UseTLS: cfg.SMTP.UseTLS}, nil
	default:
		return nil, fmt.Errorf("%w: unknown email provider %q", errEmailPermanent, cfg.Provider)
	}
}

func (w *EmailWorker) loadUser(ctx context.Context, userID string) (*recipient, error) {
	u := &recipient{id: userID}
	err := w.DB.QueryRowContext(ctx, `
		SELECT COALESCE(p.name, ''), COALESCE(p.locale, '')
		FROM system.users u
		LEFT JOIN system.user_profiles p ON p.user_id = u.id
		WHERE u.id = $1
	`, userID).Scan(&u.name, &u.locale)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("%w: recipient user %s no longer exists", errEmailPermanent, userID)
	}
	if err != nil {
		return nil, fmt.Errorf("load email recipient: %w", err)
	}
	return u, nil
}

type emailContent struct {
	subject, html, text string
}

// render renders d's email subject, HTML body and plain-text body from
// its module's email templates (notification-system.md §5), each part
// falling back to the notification's in-app title, body and action link
// when its template is absent.
func (w *EmailWorker) render(d *emailDelivery, locale string, vars map[string]any) (emailContent, error) {
	c := emailContent{subject: d.title}
	name := strings.TrimPrefix(d.typ, d.module+".")
	snapshot := w.Registry.Snapshot()
	resolve := func(channel string) (string, *notiftemplate.Template, bool) {
		return resolveTemplate(snapshot, d.module, name, channel, locale)
	}

	if matched, tmpl, ok := resolve(notiftemplate.ChannelEmailSubject); ok {
		rendered, err := notiftemplate.Render(tmpl, matched, jsonEscapedStrings(vars))
		if err != nil {
			return c, fmt.Errorf("%w: email subject: %w", ErrRenderFailed, err)
		}
		var subject struct {
			Subject string `json:"subject"`
		}
		if err := json.Unmarshal([]byte(rendered), &subject); err != nil {
			return c, fmt.Errorf("%w: email subject output is not a JSON object: %w", ErrRenderFailed, err)
		}
		if subject.Subject != "" {
			c.subject = subject.Subject
		}
	}

	actionURL, _ := vars["ActionURL"].(string)
	if matched, tmpl, ok := resolve(notifications.ChannelEmail); ok {
		rendered, err := notiftemplate.Render(tmpl, matched, vars)
		if err != nil {
			return c, fmt.Errorf("%w: email html: %w", ErrRenderFailed, err)
		}
		c.html = rendered
	} else {
		var buf bytes.Buffer
		if err := defaultEmailBody.Execute(&buf, map[string]any{"Title": d.title, "Body": d.body, "ActionURL": actionURL, "TenantName": vars["TenantName"]}); err != nil {
			return c, fmt.Errorf("%w: default email body: %w", ErrRenderFailed, err)
		}
		c.html = buf.String()
	}

	if matched, tmpl, ok := resolve(notiftemplate.ChannelEmailText); ok {
		rendered, err := notiftemplate.Render(tmpl, matched, vars)
		if err != nil {
			return c, fmt.Errorf("%w: email text: %w", ErrRenderFailed, err)
		}
		c.text = rendered
	} else {
		c.text = strings.Join(nonBlank(d.title, d.body, actionURL), "\n\n") + "\n"
	}
	return c, nil
}

// layout is the tenant's configured layout_template, "{theme_module}/{path}"
// inside an installed theme module's package, or the engine's default.
func (w *EmailWorker) layout(ref string) (*htmltemplate.Template, error) {
	if ref == "" {
		return defaultLayout, nil
	}
	moduleName, path, ok := strings.Cut(ref, "/")
	if !ok || path == "" {
		return nil, fmt.Errorf("layout_template %q is not {theme_module}/{path}", ref)
	}
	snapshot := w.Registry.Snapshot()
	if snapshot == nil {
		return nil, fmt.Errorf("layout_template %q: no module registry", ref)
	}
	mod, ok := snapshot.Modules()[moduleName]
	if !ok || mod.Manifest.Type != "theme" || mod.PackagePath == "" {
		return nil, fmt.Errorf("layout_template %q: %q is not an installed theme module", ref, moduleName)
	}
	src, err := notiftemplate.ReadPackageFile(mod.PackagePath, path)
	if err != nil {
		return nil, fmt.Errorf("layout_template %q: %w", ref, err)
	}
	tmpl, err := htmltemplate.New("layout").Parse(string(src))
	if err != nil {
		return nil, fmt.Errorf("layout_template %q: %w", ref, err)
	}
	return tmpl, nil
}

// messageID carries the notification ID, on the sender's domain.
func messageID(notificationID, fromAddr, fallbackDomain string) string {
	domain := fallbackDomain
	if _, d, ok := strings.Cut(fromAddr, "@"); ok && d != "" {
		domain = d
	}
	return "<" + notificationID + "@" + domain + ">"
}

// absoluteURL resolves ref, a path or a full URL, against base.
func absoluteURL(base, ref string) string {
	if strings.HasPrefix(ref, "/") {
		return base + ref
	}
	return ref
}

func nonBlank(parts ...string) []string {
	out := parts[:0]
	for _, p := range parts {
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}
