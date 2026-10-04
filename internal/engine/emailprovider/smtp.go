package emailprovider

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"net/smtp"
	"net/textproto"
	"slices"
	"strconv"
	"syscall"
	"time"
)

const ProviderSMTP = "smtp"

// SMTP sends through a tenant's own SMTP server. With UseTLS, port 465 is
// implicit TLS and any other port must offer STARTTLS; without it the
// connection stays in plaintext.
type SMTP struct {
	Host              string
	Port              int
	User              string
	Password          string
	UseTLS            bool
	AllowPrivateHosts bool
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
	dialer := &net.Dialer{}
	if !s.AllowPrivateHosts {
		// Control runs after DNS resolution on the exact address about to be
		// connected, including fallback addresses, preventing DNS rebinding.
		dialer.Control = func(_, address string, _ syscall.RawConn) error {
			host, _, err := net.SplitHostPort(address)
			if err != nil {
				return fmt.Errorf("%w: invalid smtp destination", ErrPermanent)
			}

			ip, err := netip.ParseAddr(host)
			if err != nil || blockedSMTPIP(ip) {
				return fmt.Errorf("%w: smtp destination is not a public address", ErrPermanent)
			}

			return nil
		}
	}

	var conn net.Conn
	var err error
	if s.UseTLS && s.Port == 465 {
		conn, err = (&tls.Dialer{NetDialer: dialer, Config: tlsConfig}).DialContext(ctx, "tcp", addr)
	} else {
		conn, err = dialer.DialContext(ctx, "tcp", addr)
	}
	if err != nil {
		return "", fmt.Errorf("%w: dial smtp server: %w", ErrConnection, err)
	}
	defer func() { _ = conn.Close() }()
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}

	client, err := smtp.NewClient(conn, s.Host)
	if err != nil {
		return "", classifySMTP("create smtp client", err)
	}
	defer func() { _ = client.Close() }()

	if s.UseTLS && s.Port != 465 {
		if ok, _ := client.Extension("STARTTLS"); !ok {
			return "", fmt.Errorf("%w: %w: smtp server %s does not offer STARTTLS", ErrConnection, ErrPermanent, addr)
		}
		if err := client.StartTLS(tlsConfig); err != nil {
			return "", classifySMTP("smtp STARTTLS", err)
		}
	}
	if s.User != "" {
		if err := client.Auth(smtp.PlainAuth("", s.User, s.Password, s.Host)); err != nil {
			if _, isNet := errors.AsType[net.Error](err); !isNet {
				if _, isReply := errors.AsType[*textproto.Error](err); !isReply {
					// net/smtp refused locally, e.g. a password over a
					// plaintext connection to a non-localhost server.
					return "", fmt.Errorf("%w: %w: smtp auth: %w", ErrConnection, ErrPermanent, err)
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
		return "", classifySMTP("write smtp message", err)
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
	if tpErr, ok := errors.AsType[*textproto.Error](err); ok {
		if tpErr.Code >= 500 {
			return fmt.Errorf("%w: %s: %w", ErrPermanent, step, err)
		}

		return fmt.Errorf("%s: %w", step, err)
	}

	return fmt.Errorf("%w: %s: %w", ErrConnection, step, err)
}

// IsGlobalUnicast includes special-purpose ranges that cannot be used as
// public SMTP destinations. Translation ranges can hide a private IPv4 address.
var blockedSMTPRanges = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),
	netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("192.0.0.0/24"),
	netip.MustParsePrefix("192.0.2.0/24"),
	netip.MustParsePrefix("192.88.99.0/24"),
	netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("198.51.100.0/24"),
	netip.MustParsePrefix("203.0.113.0/24"),
	netip.MustParsePrefix("240.0.0.0/4"),
	netip.MustParsePrefix("168.63.129.16/32"),
	netip.MustParsePrefix("2001::/23"),
	netip.MustParsePrefix("2001:db8::/32"),
	netip.MustParsePrefix("2002::/16"),
	netip.MustParsePrefix("3ffe::/16"),
	netip.MustParsePrefix("3fff::/20"),
}

var publicSMTPIPv6Range = netip.MustParsePrefix("2000::/3")

func blockedSMTPIP(ip netip.Addr) bool {
	ip = ip.Unmap()
	if !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.Zone() != "" {
		return true
	}

	if ip.Is6() && !publicSMTPIPv6Range.Contains(ip) {
		return true
	}

	return slices.ContainsFunc(blockedSMTPRanges, func(prefix netip.Prefix) bool { return prefix.Contains(ip) })
}
