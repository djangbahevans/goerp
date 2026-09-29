package notify

import (
	"bytes"
	"cmp"
	"context"
	"fmt"
	htmltemplate "html/template"
	"net/http"
	"time"
	"uuid"

	"github.com/djangbahevans/goerp/internal/engine/emailprovider"
	"github.com/djangbahevans/goerp/internal/engine/mailer"
	"github.com/djangbahevans/goerp/internal/engine/notifconfig"
)

var testEmailBody = htmltemplate.Must(htmltemplate.New("body").Parse(
	`<p><strong>Your email settings work</strong></p><p>This is a test email from {{.TenantName}}. Notification emails will be sent with these settings.</p>`))

// EmailTester sends the tenant settings page's test email
// (shell-ux.md §5.5 "Notification delivery API") through the same
// provider adapters and layout as email_send, without a notification or
// delivery row behind it.
type EmailTester struct {
	Registry       Registry
	Tenants        EmailTenants
	AppBaseURL     string
	PlatformDomain string
	ResendBaseURL  string
	HTTPClient     *http.Client
}

// ConfigError is a test email that cannot be sent because cfg is
// incomplete or invalid, as opposed to one the provider rejected. Field
// is the tenant settings field at fault, such as "email.layout_template".
type ConfigError struct {
	Field string
	Err   error
}

func (e *ConfigError) Error() string { return e.Err.Error() }
func (e *ConfigError) Unwrap() error { return e.Err }

// Send renders a test email with cfg's layout and sends it to to through
// the provider cfg selects, returning that provider's name. A cfg that
// cannot send is a *ConfigError; any other error is the provider's.
func (t *EmailTester) Send(ctx context.Context, tenantID string, cfg notifconfig.EmailConfig, to string) (string, error) {
	sender, err := emailSender(cfg, t.ResendBaseURL, t.HTTPClient)
	if err != nil {
		return "", &ConfigError{Field: "email", Err: err}
	}
	layout, err := ResolveEmailLayout(t.Registry.Snapshot(), cfg.LayoutTemplate)
	if err != nil {
		return "", &ConfigError{Field: "email.layout_template", Err: err}
	}

	tn, err := t.Tenants.GetByID(ctx, tenantID)
	if err != nil {
		return "", fmt.Errorf("load tenant: %w", err)
	}
	profile, err := t.Tenants.GetProfile(ctx, tenantID)
	if err != nil {
		return "", fmt.Errorf("load tenant profile: %w", err)
	}
	base := mailer.TenantBaseURL(t.AppBaseURL, t.PlatformDomain, tn.Slug)
	logoURL := ""
	if profile.LogoURL != nil {
		logoURL = absoluteURL(base, *profile.LogoURL)
	}

	var content bytes.Buffer
	if err := testEmailBody.Execute(&content, map[string]any{"TenantName": tn.Name}); err != nil {
		return "", fmt.Errorf("render test email body: %w", err)
	}
	// There is no notification type to unsubscribe from, so the layout's
	// unsubscribe link points at the tenant's app.
	var html bytes.Buffer
	if err := layout.Execute(&html, map[string]any{
		"Content":        htmltemplate.HTML(content.String()),
		"TenantName":     tn.Name,
		"TenantLogoURL":  logoURL,
		"UnsubscribeURL": base,
		"Year":           time.Now().Year(),
	}); err != nil {
		return "", &ConfigError{Field: "email.layout_template", Err: fmt.Errorf("render email layout: %w", err)}
	}

	id := uuid.New().String()
	msg := emailprovider.Message{
		FromName:  cmp.Or(cfg.FromName, tn.Name),
		FromAddr:  cfg.FromAddr,
		ReplyTo:   cfg.ReplyTo,
		To:        to,
		Subject:   "Test email from " + tn.Name,
		HTML:      html.String(),
		Text:      "Your email settings work.\n\nThis is a test email from " + tn.Name + ". Notification emails will be sent with these settings.\n",
		MessageID: messageID(id, cfg.FromAddr, t.PlatformDomain),
		Headers: []emailprovider.Header{
			{Name: "X-GoERP-Tenant", Value: tn.Slug},
			{Name: "X-GoERP-Type", Value: "engine.test_email"},
		},
	}
	if _, err := sender.Send(ctx, msg); err != nil {
		return sender.Name(), err
	}
	return sender.Name(), nil
}
