// Package mailer is the minimum SMTP sender goerp#148's invite flow needs
// (auth-internals.md §3, template auth.user_invited) — one hardcoded
// template, one SMTP adapter, no tenant-configurable provider selection.
// The general notification system (notification-system.md §10) is
// separate, larger, unbuilt scope.
package mailer

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"net/smtp"
	"net/url"
)

type Config struct {
	Host string
	Port int
	User string
	Pass string
	From string
	// BaseURL is the main application server's externally-reachable
	// address, used to build the accept-invite link.
	BaseURL string
}

// SMTPMailer implements internal/engine/invite.Mailer.
type SMTPMailer struct {
	cfg Config
}

func New(cfg Config) *SMTPMailer {
	return &SMTPMailer{cfg: cfg}
}

// SendInvite matches auth-internals.md §3's two subject/body variants,
// depending on whether the invitee already has a system.users row.
func (m *SMTPMailer) SendInvite(ctx context.Context, email, tenantSlug, rawToken string, isNewUser bool) error {
	link := fmt.Sprintf("%s/auth/accept-invite?token=%s&tenant=%s",
		m.cfg.BaseURL, url.QueryEscape(rawToken), url.QueryEscape(tenantSlug))

	var subject, text, html string
	if isNewUser {
		subject = fmt.Sprintf("You've been invited to %s — click to set up your account", tenantSlug)
		text = fmt.Sprintf("You've been invited to %s.\n\nSet up your account:\n%s\n", tenantSlug, link)
		html = fmt.Sprintf(`<p>You've been invited to <strong>%s</strong>.</p><p><a href="%s">Set up your account</a></p>`, tenantSlug, link)
	} else {
		subject = fmt.Sprintf("You've been invited to join %s — click to accept", tenantSlug)
		text = fmt.Sprintf("You've been invited to join %s.\n\nAccept the invite:\n%s\n", tenantSlug, link)
		html = fmt.Sprintf(`<p>You've been invited to join <strong>%s</strong>.</p><p><a href="%s">Accept the invite</a></p>`, tenantSlug, link)
	}

	return m.send(ctx, email, subject, text, html)
}

// SendMFAReset notifies email that an administrator reset their MFA
// enrollment — auth-internals.md §8 "Account recovery when all factors
// are lost" step 4. One hardcoded template, same minimal-slice approach
// SendInvite already takes for this package.
func (m *SMTPMailer) SendMFAReset(ctx context.Context, email string) error {
	const subject = "Your multi-factor authentication has been reset"
	text := "An administrator has reset your multi-factor authentication (MFA) settings.\n\n" +
		"You'll need to enroll a new MFA factor the next time this is required.\n\n" +
		"If you did not expect this, contact your administrator immediately.\n"
	html := "<p>An administrator has reset your multi-factor authentication (MFA) settings.</p>" +
		"<p>You'll need to enroll a new MFA factor the next time this is required.</p>" +
		"<p>If you did not expect this, contact your administrator immediately.</p>"

	return m.send(ctx, email, subject, text, html)
}

// SendPasswordReset carries the reset link — auth-internals.md §3
// "Password reset", template auth.password_reset.
func (m *SMTPMailer) SendPasswordReset(ctx context.Context, email, tenantSlug, rawToken string) error {
	link := fmt.Sprintf("%s/auth/reset-password?token=%s&tenant=%s",
		m.cfg.BaseURL, url.QueryEscape(rawToken), url.QueryEscape(tenantSlug))

	const subject = "Reset your password"
	text := fmt.Sprintf("A password reset was requested for your account.\n\n"+
		"Set a new password (this link expires in 1 hour):\n%s\n\n"+
		"If you did not request this, you can ignore this email.\n", link)
	html := fmt.Sprintf("<p>A password reset was requested for your account.</p>"+
		`<p><a href="%s">Set a new password</a> (this link expires in 1 hour).</p>`+
		"<p>If you did not request this, you can ignore this email.</p>", link)

	return m.send(ctx, email, subject, text, html)
}

// SendPasswordResetConfirmed — template auth.password_reset_confirmed.
func (m *SMTPMailer) SendPasswordResetConfirmed(ctx context.Context, email string) error {
	const subject = "Your password has been changed"
	text := "The password for your account was just reset, and you have been signed out everywhere.\n\n" +
		"If you did not do this, contact your administrator immediately.\n"
	html := "<p>The password for your account was just reset, and you have been signed out everywhere.</p>" +
		"<p>If you did not do this, contact your administrator immediately.</p>"

	return m.send(ctx, email, subject, text, html)
}

// SendVerifyEmail carries the email-verification link for a self-registered
// account — auth-internals.md §3, template auth.verify_email. The link
// carries the tenant so confirming can sign the user into it.
func (m *SMTPMailer) SendVerifyEmail(ctx context.Context, email, tenantSlug, rawToken string) error {
	link := fmt.Sprintf("%s/auth/verify-email?token=%s&tenant=%s",
		m.cfg.BaseURL, url.QueryEscape(rawToken), url.QueryEscape(tenantSlug))

	const subject = "Verify your email address"
	text := fmt.Sprintf("Confirm your email address to finish setting up your account "+
		"(this link expires in 24 hours):\n%s\n\n"+
		"If you didn't create an account, you can ignore this email.\n", link)
	html := fmt.Sprintf(`<p><a href="%s">Confirm your email address</a> to finish setting up your account `+
		"(this link expires in 24 hours).</p>"+
		"<p>If you didn't create an account, you can ignore this email.</p>", link)

	return m.send(ctx, email, subject, text, html)
}

// SendPasswordChanged — template auth.password_changed.
func (m *SMTPMailer) SendPasswordChanged(ctx context.Context, email string) error {
	const subject = "Your password has been changed"
	text := "The password for your account was just changed, and your other sessions were signed out.\n\n" +
		"If you did not do this, reset your password and contact your administrator immediately.\n"
	html := "<p>The password for your account was just changed, and your other sessions were signed out.</p>" +
		"<p>If you did not do this, reset your password and contact your administrator immediately.</p>"

	return m.send(ctx, email, subject, text, html)
}

func (m *SMTPMailer) send(ctx context.Context, to, subject, text, html string) error {
	addr := fmt.Sprintf("%s:%d", m.cfg.Host, m.cfg.Port)

	var d net.Dialer
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return fmt.Errorf("dial smtp server: %w", err)
	}
	defer func() { _ = conn.Close() }()

	client, err := smtp.NewClient(conn, m.cfg.Host)
	if err != nil {
		return fmt.Errorf("create smtp client: %w", err)
	}
	defer func() { _ = client.Close() }()

	if m.cfg.User != "" {
		if err := client.Auth(smtp.PlainAuth("", m.cfg.User, m.cfg.Pass, m.cfg.Host)); err != nil {
			return fmt.Errorf("smtp auth: %w", err)
		}
	}

	if err := client.Mail(m.cfg.From); err != nil {
		return fmt.Errorf("smtp MAIL: %w", err)
	}
	if err := client.Rcpt(to); err != nil {
		return fmt.Errorf("smtp RCPT: %w", err)
	}
	w, err := client.Data()
	if err != nil {
		return fmt.Errorf("smtp DATA: %w", err)
	}
	if _, err := w.Write(buildMessage(m.cfg.From, to, subject, text, html)); err != nil {
		return fmt.Errorf("write smtp message: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("close smtp writer: %w", err)
	}

	return client.Quit()
}

const mimeBoundary = "goerp-invite-boundary"

// buildMessage produces a multipart/alternative RFC 822 message —
// net/smtp has no MIME support of its own.
func buildMessage(from, to, subject, text, html string) []byte {
	var buf bytes.Buffer
	fmt.Fprintf(&buf, "From: %s\r\n", from)
	fmt.Fprintf(&buf, "To: %s\r\n", to)
	fmt.Fprintf(&buf, "Subject: %s\r\n", subject)
	fmt.Fprintf(&buf, "MIME-Version: 1.0\r\n")
	fmt.Fprintf(&buf, "Content-Type: multipart/alternative; boundary=%q\r\n\r\n", mimeBoundary)
	fmt.Fprintf(&buf, "--%s\r\nContent-Type: text/plain; charset=UTF-8\r\n\r\n%s\r\n\r\n", mimeBoundary, text)
	fmt.Fprintf(&buf, "--%s\r\nContent-Type: text/html; charset=UTF-8\r\n\r\n%s\r\n\r\n", mimeBoundary, html)
	fmt.Fprintf(&buf, "--%s--\r\n", mimeBoundary)
	return buf.Bytes()
}
