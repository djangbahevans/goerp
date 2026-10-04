// Package emailprovider is the notification email delivery adapters
// (notification-system.md §10 "Providers"): Resend, over its HTTP API,
// and SMTP. A tenant's [notifications.email].provider picks one per send.
package emailprovider

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"mime"
	"mime/quotedprintable"
	"net/mail"
	"strings"
	"time"
)

type Header struct {
	Name, Value string
}

// Message is one rendered email to one recipient.
type Message struct {
	FromName string
	FromAddr string
	ReplyTo  string
	To       string
	Subject  string
	HTML     string
	Text     string
	// MessageID is the Message-ID header's value, angle brackets included.
	MessageID string
	Headers   []Header
	// IdempotencyKey is handed to a provider with a native idempotency
	// mechanism, so that a retried send it already accepted is not sent
	// again.
	IdempotencyKey string
}

// Sender sends a Message and returns the provider's ID for it.
type Sender interface {
	Name() string
	Send(ctx context.Context, msg Message) (providerID string, err error)
}

var (
	// ErrConnection marks transport failures whose details must stay out of
	// tenant-facing test-email responses.
	ErrConnection = errors.New("email provider connection failed")
	// ErrPermanent: the provider rejected the message in a way a retry
	// cannot fix.
	ErrPermanent = errors.New("email rejected permanently")
	// ErrQuotaExceeded: the provider refused the message for rate or quota
	// reasons; a later retry may succeed.
	ErrQuotaExceeded = errors.New("email provider quota exceeded")
	// ErrAlreadySent: the provider already accepted a message under this
	// idempotency key, with a body that differs from this one, so it was
	// sent and the provider cannot say under which ID.
	ErrAlreadySent = errors.New("email already sent under this idempotency key")
)

func (m Message) from() string {
	return (&mail.Address{Name: m.FromName, Address: m.FromAddr}).String()
}

// build renders m as an RFC 5322 multipart/alternative message.
func (m Message) build(date time.Time) []byte {
	const boundary = "goerp-notification-boundary"

	var buf bytes.Buffer
	header := func(name, value string) { fmt.Fprintf(&buf, "%s: %s\r\n", name, stripCRLF(value)) }
	header("From", m.from())
	header("To", (&mail.Address{Address: m.To}).String())
	if m.ReplyTo != "" {
		header("Reply-To", (&mail.Address{Address: m.ReplyTo}).String())
	}
	header("Subject", mime.QEncoding.Encode("utf-8", m.Subject))
	header("Date", date.Format(time.RFC1123Z))
	header("Message-ID", m.MessageID)
	for _, h := range m.Headers {
		header(h.Name, h.Value)
	}
	header("MIME-Version", "1.0")
	header("Content-Type", fmt.Sprintf("multipart/alternative; boundary=%q", boundary))
	buf.WriteString("\r\n")

	for _, part := range []struct{ contentType, body string }{
		{"text/plain; charset=UTF-8", m.Text},
		{"text/html; charset=UTF-8", m.HTML},
	} {
		fmt.Fprintf(&buf, "--%s\r\nContent-Type: %s\r\nContent-Transfer-Encoding: quoted-printable\r\n\r\n", boundary, part.contentType)
		qp := quotedprintable.NewWriter(&buf)
		_, _ = qp.Write([]byte(part.body))
		_ = qp.Close()
		buf.WriteString("\r\n")
	}
	fmt.Fprintf(&buf, "--%s--\r\n", boundary)
	return buf.Bytes()
}

// stripCRLF keeps a header value from injecting further headers.
func stripCRLF(s string) string {
	return strings.NewReplacer("\r", "", "\n", "").Replace(s)
}
