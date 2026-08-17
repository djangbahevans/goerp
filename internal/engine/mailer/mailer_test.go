package mailer

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeSMTPServer is a minimal SMTP server good enough to exercise
// SMTPMailer — no external dependency, no real Mailpit needed.
type fakeSMTPServer struct {
	ln net.Listener

	mu       sync.Mutex
	messages []receivedMessage

	requireAuth  bool
	expectedUser string
	expectedPass string
}

type receivedMessage struct {
	from string
	to   []string
	data string
}

func startFakeSMTP(t *testing.T) *fakeSMTPServer {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	s := &fakeSMTPServer{ln: ln}
	go s.serve()
	t.Cleanup(func() { _ = ln.Close() })
	return s
}

func (s *fakeSMTPServer) addr() (string, int) {
	tcpAddr := s.ln.Addr().(*net.TCPAddr)
	return tcpAddr.IP.String(), tcpAddr.Port
}

func (s *fakeSMTPServer) serve() {
	for {
		conn, err := s.ln.Accept()
		if err != nil {
			return
		}
		go s.handle(conn)
	}
}

func (s *fakeSMTPServer) handle(conn net.Conn) {
	defer func() { _ = conn.Close() }()
	r := bufio.NewReader(conn)
	w := bufio.NewWriter(conn)
	reply := func(code int, msg string) {
		_, _ = fmt.Fprintf(w, "%d %s\r\n", code, msg)
		_ = w.Flush()
	}
	replyMulti := func(lines ...string) {
		for i, line := range lines {
			sep := "-"
			if i == len(lines)-1 {
				sep = " "
			}
			_, _ = fmt.Fprintf(w, "250%s%s\r\n", sep, line)
		}
		_ = w.Flush()
	}

	reply(220, "fake.smtp ESMTP")

	var msg receivedMessage
	inData := false
	var dataBuf strings.Builder

	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		line = strings.TrimRight(line, "\r\n")

		if inData {
			if line == "." {
				inData = false
				msg.data = dataBuf.String()
				s.mu.Lock()
				s.messages = append(s.messages, msg)
				s.mu.Unlock()
				reply(250, "OK")
				msg = receivedMessage{}
				dataBuf.Reset()
				continue
			}
			dataBuf.WriteString(line)
			dataBuf.WriteString("\n")
			continue
		}

		upper := strings.ToUpper(line)
		switch {
		case strings.HasPrefix(upper, "EHLO"), strings.HasPrefix(upper, "HELO"):
			if s.requireAuth {
				replyMulti("hello", "AUTH PLAIN")
			} else {
				reply(250, "hello")
			}
		case strings.HasPrefix(upper, "AUTH PLAIN"):
			payload := strings.TrimSpace(line[len("AUTH PLAIN"):])
			decoded, err := base64.StdEncoding.DecodeString(payload)
			if err != nil {
				reply(501, "bad base64")
				continue
			}
			parts := strings.SplitN(string(decoded), "\x00", 3)
			if len(parts) != 3 || parts[1] != s.expectedUser || parts[2] != s.expectedPass {
				reply(535, "auth failed")
				continue
			}
			reply(235, "authenticated")
		case strings.HasPrefix(upper, "MAIL FROM:"):
			msg.from = strings.TrimSpace(line[len("MAIL FROM:"):])
			reply(250, "OK")
		case strings.HasPrefix(upper, "RCPT TO:"):
			msg.to = append(msg.to, strings.TrimSpace(line[len("RCPT TO:"):]))
			reply(250, "OK")
		case upper == "DATA":
			inData = true
			reply(354, "start mail input")
		case upper == "QUIT":
			reply(221, "bye")
			return
		default:
			reply(500, "unrecognized")
		}
	}
}

func (s *fakeSMTPServer) lastMessage() (receivedMessage, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.messages) == 0 {
		return receivedMessage{}, false
	}
	return s.messages[len(s.messages)-1], true
}

func newTestMailer(t *testing.T, srv *fakeSMTPServer, user, pass string) *SMTPMailer {
	t.Helper()
	ip, port := srv.addr()
	return New(Config{
		Host:    ip,
		Port:    port,
		User:    user,
		Pass:    pass,
		From:    "noreply@goerp.local",
		BaseURL: "http://localhost:8080",
	})
}

func waitForMessage(t *testing.T, srv *fakeSMTPServer) receivedMessage {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if msg, ok := srv.lastMessage(); ok {
			return msg
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("timed out waiting for the fake SMTP server to receive a message")
	return receivedMessage{}
}

func TestSMTPMailer_SendInvite_NewUser(t *testing.T) {
	srv := startFakeSMTP(t)
	m := newTestMailer(t, srv, "", "")

	if err := m.SendInvite(context.Background(), "kwame@example.com", "acmecorp", "raw-token-123", true); err != nil {
		t.Fatalf("SendInvite() error: %v", err)
	}

	msg := waitForMessage(t, srv)
	if msg.from != "<noreply@goerp.local>" {
		t.Errorf("from = %q, want %q", msg.from, "<noreply@goerp.local>")
	}
	if len(msg.to) != 1 || msg.to[0] != "<kwame@example.com>" {
		t.Errorf("to = %v, want [<kwame@example.com>]", msg.to)
	}
	if !strings.Contains(msg.data, "set up your account") {
		t.Errorf("new-user email should mention setting up an account, got: %s", msg.data)
	}
	if !strings.Contains(msg.data, "token=raw-token-123") || !strings.Contains(msg.data, "tenant=acmecorp") {
		t.Errorf("email should contain the accept-invite link with token and tenant, got: %s", msg.data)
	}
}

func TestSMTPMailer_SendInvite_ExistingUser(t *testing.T) {
	srv := startFakeSMTP(t)
	m := newTestMailer(t, srv, "", "")

	if err := m.SendInvite(context.Background(), "kwame@example.com", "acmecorp", "raw-token-123", false); err != nil {
		t.Fatalf("SendInvite() error: %v", err)
	}

	msg := waitForMessage(t, srv)
	if !strings.Contains(msg.data, "invited to join acmecorp") {
		t.Errorf("existing-user email should say \"invited to join\", got: %s", msg.data)
	}
	if strings.Contains(msg.data, "set up your account") {
		t.Errorf("existing-user email should not use the new-user phrasing, got: %s", msg.data)
	}
}

func TestSMTPMailer_SendInvite_WithAuth(t *testing.T) {
	srv := startFakeSMTP(t)
	srv.requireAuth = true
	srv.expectedUser = "operator"
	srv.expectedPass = "hunter2"
	m := newTestMailer(t, srv, "operator", "hunter2")

	if err := m.SendInvite(context.Background(), "kwame@example.com", "acmecorp", "raw-token-123", true); err != nil {
		t.Fatalf("SendInvite() error: %v", err)
	}
	waitForMessage(t, srv)
}

func TestSMTPMailer_WrongCredentialsFail(t *testing.T) {
	srv := startFakeSMTP(t)
	srv.requireAuth = true
	srv.expectedUser = "operator"
	srv.expectedPass = "hunter2"
	m := newTestMailer(t, srv, "operator", "wrong-password")

	if err := m.SendInvite(context.Background(), "kwame@example.com", "acmecorp", "raw-token-123", true); err == nil {
		t.Fatal("SendInvite() error = nil, want an auth failure")
	}
}

func TestSMTPMailer_UnreachableServerFails(t *testing.T) {
	m := New(Config{Host: "127.0.0.1", Port: 1, From: "noreply@goerp.local", BaseURL: "http://localhost:8080"})

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	if err := m.SendInvite(ctx, "kwame@example.com", "acmecorp", "raw-token-123", true); err == nil {
		t.Fatal("SendInvite() error = nil, want a connection failure")
	}
}

// TestSMTPMailer_AgainstRealMailpit is the actual goerp#166 acceptance
// check: with no SMTP config set (Config zero value plus the same
// localhost:1025 default GOERP_SMTP_HOST/PORT resolve to), an invite sent
// through this mailer is visible in the real Mailpit instance
// compose.dev.yml starts. Skips if Mailpit isn't reachable.
func TestSMTPMailer_AgainstRealMailpit(t *testing.T) {
	conn, err := net.DialTimeout("tcp", "localhost:1025", 500*time.Millisecond)
	if err != nil {
		t.Skipf("mailpit not reachable at localhost:1025 (start compose.dev.yml): %v", err)
	}
	_ = conn.Close()

	m := New(Config{
		Host:    "localhost",
		Port:    1025,
		From:    "noreply@goerp.local",
		BaseURL: "http://localhost:8080",
	})

	uniqueToken := fmt.Sprintf("smoke-test-%d", time.Now().UnixNano())
	if err := m.SendInvite(context.Background(), "kwame@example.com", "acmecorp", uniqueToken, true); err != nil {
		t.Fatalf("SendInvite() against real Mailpit error: %v", err)
	}

	resp, err := http.Get("http://localhost:8025/api/v1/messages?query=" + url.QueryEscape("to:kwame@example.com"))
	if err != nil {
		t.Fatalf("query mailpit API: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	var result struct {
		Messages []struct {
			Snippet string `json:"Snippet"`
		} `json:"messages"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("decode mailpit response: %v", err)
	}
	if len(result.Messages) == 0 {
		t.Fatal("mailpit reports no messages to kwame@example.com after SendInvite()")
	}
}

func TestBuildMessage_IsValidMultipartAlternative(t *testing.T) {
	msg := string(buildMessage("noreply@goerp.local", "kwame@example.com", "Subject line", "plain body", "<p>html body</p>"))

	for _, want := range []string{
		"From: noreply@goerp.local",
		"To: kwame@example.com",
		"Subject: Subject line",
		"Content-Type: multipart/alternative",
		"Content-Type: text/plain",
		"plain body",
		"Content-Type: text/html",
		"<p>html body</p>",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("message missing %q:\n%s", want, msg)
		}
	}
}
