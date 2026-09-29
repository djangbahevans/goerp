package emailprovider

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/smtp"
	"net/textproto"
	"strconv"
	"time"
)

const ProviderSMTP = "smtp"

// SMTP sends through a tenant's own SMTP server. With UseTLS, port 465 is
// implicit TLS and any other port must offer STARTTLS; without it the
// connection stays in plaintext.
type SMTP struct {
	Host     string
	Port     int
	User     string
	Password string
	UseTLS   bool
	// Now defaults to time.Now; it stamps the Date header.
	Now func() time.Time
}

func (s *SMTP) Name() string { return ProviderSMTP }

// Send delivers msg and returns its Message-ID as the provider ID: SMTP
// has no provider-side message ID of its own. SMTP also has no
// idempotency mechanism, so msg.IdempotencyKey is not used.
func (s *SMTP) Send(ctx context.Context, msg Message) (string, error) {
	addr := net.JoinHostPort(s.Host, strconv.Itoa(s.Port))
	tlsConfig := &tls.Config{ServerName: s.Host, MinVersion: tls.VersionTLS12}

	var conn net.Conn
	var err error
	if s.UseTLS && s.Port == 465 {
		conn, err = (&tls.Dialer{Config: tlsConfig}).DialContext(ctx, "tcp", addr)
	} else {
		conn, err = (&net.Dialer{}).DialContext(ctx, "tcp", addr)
	}
	if err != nil {
		return "", fmt.Errorf("dial smtp server: %w", err)
	}
	defer func() { _ = conn.Close() }()
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}

	client, err := smtp.NewClient(conn, s.Host)
	if err != nil {
		return "", fmt.Errorf("create smtp client: %w", err)
	}
	defer func() { _ = client.Close() }()

	if s.UseTLS && s.Port != 465 {
		if ok, _ := client.Extension("STARTTLS"); !ok {
			return "", fmt.Errorf("%w: smtp server %s does not offer STARTTLS", ErrPermanent, addr)
		}
		if err := client.StartTLS(tlsConfig); err != nil {
			return "", fmt.Errorf("smtp STARTTLS: %w", err)
		}
	}
	if s.User != "" {
		if err := client.Auth(smtp.PlainAuth("", s.User, s.Password, s.Host)); err != nil {
			if _, isNet := errors.AsType[net.Error](err); !isNet {
				if _, isReply := errors.AsType[*textproto.Error](err); !isReply {
					// net/smtp refused locally, e.g. a password over a
					// plaintext connection to a non-localhost server.
					return "", fmt.Errorf("%w: smtp auth: %w", ErrPermanent, err)
				}
			}
			return "", classifySMTP("smtp auth", err)
		}
	}
	if err := client.Mail(msg.FromAddr); err != nil {
		return "", classifySMTP("smtp MAIL", err)
	}
	if err := client.Rcpt(msg.To); err != nil {
		return "", classifySMTP("smtp RCPT", err)
	}
	w, err := client.Data()
	if err != nil {
		return "", classifySMTP("smtp DATA", err)
	}
	now := time.Now
	if s.Now != nil {
		now = s.Now
	}
	if _, err := w.Write(msg.build(now())); err != nil {
		return "", fmt.Errorf("write smtp message: %w", err)
	}
	if err := w.Close(); err != nil {
		return "", classifySMTP("smtp end of DATA", err)
	}
	_ = client.Quit()
	return msg.MessageID, nil
}

// classifySMTP marks a 5xx reply permanent; a 4xx reply is transient by
// definition (RFC 5321 §4.2.1).
func classifySMTP(step string, err error) error {
	if tpErr, ok := errors.AsType[*textproto.Error](err); ok && tpErr.Code >= 500 {
		return fmt.Errorf("%w: %s: %w", ErrPermanent, step, err)
	}
	return fmt.Errorf("%s: %w", step, err)
}
