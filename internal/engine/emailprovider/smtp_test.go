package emailprovider

import (
	"context"
	"errors"
	"io"
	"net"
	"net/netip"
	"net/textproto"
	"testing"
	"time"
)

func TestSMTP_PublicDestinationPolicy(t *testing.T) {
	for _, host := range []string{
		"0.0.0.0", "0.1.2.3", "10.0.0.1", "100.64.0.1", "127.0.0.1",
		"169.254.169.254", "172.16.0.1", "192.168.1.1", "168.63.129.16",
		"192.0.0.1", "192.0.2.1", "192.88.99.1", "198.18.0.1", "198.51.100.1",
		"203.0.113.1", "224.0.0.1", "240.0.0.1", "255.255.255.255",
		"::", "::1", "::ffff:127.0.0.1", "::ffff:169.254.169.254",
		"fc00::1", "fd00:ec2::254", "fe80::1", "fe80::1%eth0", "fec0::1", "ff02::1",
		"64:ff9b::a00:1", "64:ff9b:1::a00:1", "100::1", "2001::1",
		"2001:db8::1", "2002:7f00:1::1", "3fff::1", "5f00::1",
	} {
		t.Run(host, func(t *testing.T) {
			if !blockedSMTPIP(netip.MustParseAddr(host)) {
				t.Fatalf("SMTP destination %s allowed, want blocked", host)
			}
		})
	}

	for _, host := range []string{"8.8.8.8", "1.1.1.1", "::ffff:8.8.8.8", "2606:4700:4700::1111"} {
		t.Run(host, func(t *testing.T) {
			if blockedSMTPIP(netip.MustParseAddr(host)) {
				t.Fatalf("public SMTP destination %s blocked", host)
			}
		})
	}
}

func TestSMTP_BlockedDestinationsNeverConnect(t *testing.T) {
	listener, err := net.ListenTCP("tcp4", &net.TCPAddr{IP: net.ParseIP("127.0.0.1")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })

	for _, host := range []string{"127.0.0.1", "localhost", "::ffff:127.0.0.1"} {
		for _, useTLS := range []bool{false, true} {
			sender := &SMTP{
				Host:   host,
				Port:   listener.Addr().(*net.TCPAddr).Port,
				UseTLS: useTLS,
			}
			ctx, cancel := context.WithTimeout(t.Context(), time.Second)

			_, err := sender.Send(ctx, testMessage("blocked@example.test"))
			cancel()

			if !errors.Is(err, ErrPermanent) || !errors.Is(err, ErrConnection) {
				t.Fatalf("Send(%s, TLS=%t) = %v, want permanent connection-policy rejection", host, useTLS, err)
			}
		}
	}

	if err := listener.SetDeadline(time.Now().Add(50 * time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	conn, err := listener.Accept()
	if err == nil {
		_ = conn.Close()
		t.Fatal("blocked SMTP destination received a connection")
	}
	if ne, ok := errors.AsType[net.Error](err); !ok || !ne.Timeout() {
		t.Fatalf("Accept() = %v, want timeout without a connection", err)
	}
}

func TestSMTP_ImplicitTLSRejectsBlockedDestinations(t *testing.T) {
	sender := &SMTP{Host: "localhost", Port: 465, UseTLS: true}
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()

	_, err := sender.Send(ctx, testMessage("blocked@example.test"))
	if !errors.Is(err, ErrPermanent) || !errors.Is(err, ErrConnection) {
		t.Fatalf("implicit TLS Send() = %v, want permanent connection-policy rejection", err)
	}
}

func TestSMTP_ErrorClassification(t *testing.T) {
	for _, tc := range []struct {
		name                  string
		err                   error
		connection, permanent bool
	}{
		{"connection closed", io.EOF, true, false},
		{"canceled", context.Canceled, true, false},
		{"temporary rejection", &textproto.Error{Code: 450, Msg: "mailbox busy"}, false, false},
		{"permanent rejection", &textproto.Error{Code: 550, Msg: "mailbox unavailable"}, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := classifySMTP("smtp RCPT", tc.err)
			if errors.Is(err, ErrConnection) != tc.connection || errors.Is(err, ErrPermanent) != tc.permanent {
				t.Fatalf("classifySMTP() = %v, want connection=%t permanent=%t", err, tc.connection, tc.permanent)
			}
			if !errors.Is(err, tc.err) {
				t.Fatalf("classifySMTP() = %v, want wrapped cause %v", err, tc.err)
			}
		})
	}
}
