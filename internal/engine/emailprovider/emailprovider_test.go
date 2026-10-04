package emailprovider

import (
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

const (
	mailpitSMTPHost = "localhost"
	mailpitSMTPPort = 1025
	mailpitAPI      = "http://localhost:8025/api/v1"
)

func testMessage(to string) Message {
	return Message{
		FromName:  "Acme Corp",
		FromAddr:  "noreply@acme.test",
		ReplyTo:   "support@acme.test",
		To:        to,
		Subject:   "Order ORD-42 confirmed — Acme",
		HTML:      "<p>Hello <strong>Ama</strong></p>",
		Text:      "Hello Ama",
		MessageID: "<n-1@acme.test>",
		Headers: []Header{
			{Name: "List-Unsubscribe", Value: "<https://acme.test/_notif/unsubscribe?token=t>"},
			{Name: "X-GoERP-Type", Value: "sales.order_confirmed"},
		},
		IdempotencyKey: "n-1:email:" + to,
	}
}

type resendCall struct {
	auth, idempotencyKey string
	body                 []byte
}

// resendStub answers POST /emails with status and body, recording each
// request.
func resendStub(t *testing.T, status int, body string) (*Resend, *[]resendCall) {
	t.Helper()
	var mu sync.Mutex
	calls := &[]resendCall{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		mu.Lock()
		*calls = append(*calls, resendCall{r.Header.Get("Authorization"), r.Header.Get("Idempotency-Key"), raw})
		mu.Unlock()
		if r.Method != http.MethodPost || r.URL.Path != "/emails" {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)
	return &Resend{APIKey: "re_key", BaseURL: srv.URL}, calls
}

func TestResend_SendsMessageWithIdempotencyKey(t *testing.T) {
	r, calls := resendStub(t, http.StatusOK, `{"id":"re_123"}`)
	msg := testMessage("ama@example.test")

	id, err := r.Send(t.Context(), msg)
	if err != nil || id != "re_123" {
		t.Fatalf("Send() = %q, %v, want re_123", id, err)
	}
	if _, err := r.Send(t.Context(), msg); err != nil {
		t.Fatal(err)
	}

	c := (*calls)[0]
	if c.auth != "Bearer re_key" || c.idempotencyKey != msg.IdempotencyKey {
		t.Errorf("auth/idempotency key = %q/%q", c.auth, c.idempotencyKey)
	}
	if string((*calls)[1].body) != string(c.body) {
		t.Errorf("a resend of the same message has a different body:\n%s\n%s", c.body, (*calls)[1].body)
	}
	var req resendRequest
	if err := json.Unmarshal(c.body, &req); err != nil {
		t.Fatal(err)
	}
	if req.From != `"Acme Corp" <noreply@acme.test>` || req.To[0] != msg.To || req.Subject != msg.Subject ||
		req.HTML != msg.HTML || req.Text != msg.Text || req.ReplyTo != msg.ReplyTo {
		t.Errorf("request = %+v", req)
	}
	if req.Headers["Message-ID"] != msg.MessageID || req.Headers["X-GoERP-Type"] != "sales.order_confirmed" || req.Headers["List-Unsubscribe"] == "" {
		t.Errorf("headers = %v", req.Headers)
	}
}

func TestResend_ClassifiesFailures(t *testing.T) {
	cases := []struct {
		status    int
		body      string
		permanent bool
		quota     bool
	}{
		{http.StatusTooManyRequests, `{"name":"rate_limit_exceeded","message":"slow down"}`, false, true},
		{http.StatusUnprocessableEntity, `{"name":"validation_error","message":"bad from"}`, true, false},
		{http.StatusConflict, `{"name":"concurrent_idempotent_requests","message":"in flight"}`, false, false},
		{http.StatusInternalServerError, `{"name":"application_error"}`, false, false},
	}
	for _, tc := range cases {
		t.Run(fmt.Sprintf("%d %s", tc.status, tc.body), func(t *testing.T) {
			r, _ := resendStub(t, tc.status, tc.body)
			_, err := r.Send(t.Context(), testMessage("a@example.test"))
			if err == nil {
				t.Fatal("Send() error = nil")
			}
			if errors.Is(err, ErrPermanent) != tc.permanent || errors.Is(err, ErrQuotaExceeded) != tc.quota {
				t.Errorf("Send() error = %v, want permanent=%v quota=%v", err, tc.permanent, tc.quota)
			}
		})
	}
}

func TestResend_ReusedKeyWithADifferentBodyIsAlreadySent(t *testing.T) {
	r, _ := resendStub(t, http.StatusConflict, `{"name":"invalid_idempotent_request","message":"payload differs"}`)
	if _, err := r.Send(t.Context(), testMessage("a@example.test")); !errors.Is(err, ErrAlreadySent) {
		t.Errorf("Send() error = %v, want ErrAlreadySent", err)
	}
}

func TestSMTP_PlaintextPasswordToARemoteServerFailsPermanently(t *testing.T) {
	// net/smtp only sends a password in plaintext to a server it names
	// localhost; 127.0.0.2 is loopback but not one of those names.
	ln, err := net.Listen("tcp", "127.0.0.2:0")
	if err != nil {
		t.Skipf("127.0.0.2 not usable: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		tp := textproto.NewConn(conn)
		_ = tp.PrintfLine("220 fake ESMTP")
		for {
			line, err := tp.ReadLine()
			if err != nil {
				return
			}
			if strings.HasPrefix(line, "EHLO") {
				_ = tp.PrintfLine("250-fake")
				_ = tp.PrintfLine("250 AUTH PLAIN")
				continue
			}
			_ = tp.PrintfLine("250 ok")
		}
	}()

	s := &SMTP{
		Host:              "127.0.0.2",
		Port:              ln.Addr().(*net.TCPAddr).Port,
		User:              "u",
		Password:          "p",
		AllowPrivateHosts: true,
	}
	if _, err := s.Send(t.Context(), testMessage("a@example.test")); !errors.Is(err, ErrPermanent) {
		t.Errorf("Send() error = %v, want ErrPermanent", err)
	}
}

func TestMessage_BuildEncodesSubjectAndStripsHeaderInjection(t *testing.T) {
	msg := testMessage("a@example.test")
	msg.Headers = append(msg.Headers, Header{Name: "X-GoERP-Tenant", Value: "acme\r\nBcc: evil@example.test"})
	raw := string(msg.build(time.Date(2026, 9, 29, 10, 0, 0, 0, time.UTC)))

	if strings.Contains(raw, "\r\nBcc:") {
		t.Errorf("header value injected a header:\n%s", raw)
	}
	if !strings.Contains(raw, "Subject: =?utf-8?q?") {
		t.Errorf("non-ASCII subject not encoded:\n%s", raw)
	}
	if !strings.Contains(raw, "Message-ID: <n-1@acme.test>\r\n") {
		t.Errorf("missing Message-ID:\n%s", raw)
	}
}

// mailpitMessage is the part of Mailpit's message API the tests read.
type mailpitMessage struct {
	ID        string `json:"ID"`
	MessageID string `json:"MessageID"`
	Subject   string `json:"Subject"`
	HTML      string `json:"HTML"`
	Text      string `json:"Text"`
}

// findMailpitMessage waits for Mailpit to hold a message to to, and
// returns it with its headers.
func findMailpitMessage(t *testing.T, to string) (mailpitMessage, map[string][]string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		var found struct {
			Messages []struct {
				ID string `json:"ID"`
			} `json:"messages"`
		}
		getJSON(t, mailpitAPI+"/search?query="+url.QueryEscape("to:"+to), &found)
		if len(found.Messages) > 0 {
			var msg mailpitMessage
			getJSON(t, mailpitAPI+"/message/"+found.Messages[0].ID, &msg)
			headers := map[string][]string{}
			getJSON(t, mailpitAPI+"/message/"+found.Messages[0].ID+"/headers", &headers)
			return msg, headers
		}
		if time.Now().After(deadline) {
			t.Fatalf("no message to %s reached mailpit", to)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func getJSON(t *testing.T, u string, out any) {
	t.Helper()
	resp, err := http.Get(u)
	if err != nil {
		t.Skipf("mailpit not reachable at %s (start compose.dev.yml): %v", mailpitAPI, err)
	}
	defer resp.Body.Close()
	if err := json.UnmarshalRead(resp.Body, out); err != nil {
		t.Fatalf("decode %s: %v", u, err)
	}
}

func TestSMTP_DeliversToMailpit(t *testing.T) {
	to := fmt.Sprintf("smtp%d@example.test", time.Now().UnixNano())
	msg := testMessage(to)
	msg.MessageID = fmt.Sprintf("<n-%d@acme.test>", time.Now().UnixNano())
	s := &SMTP{Host: mailpitSMTPHost, Port: mailpitSMTPPort, AllowPrivateHosts: true}

	id, err := s.Send(t.Context(), msg)
	if err != nil {
		if strings.Contains(err.Error(), "connection refused") {
			t.Skipf("mailpit SMTP not reachable (start compose.dev.yml): %v", err)
		}
		t.Fatalf("Send() error: %v", err)
	}
	if id != msg.MessageID {
		t.Errorf("provider id = %q, want the Message-ID %q", id, msg.MessageID)
	}

	got, headers := findMailpitMessage(t, to)
	if got.Subject != msg.Subject || !strings.Contains(got.HTML, "<strong>Ama</strong>") || strings.TrimSpace(got.Text) != msg.Text {
		t.Errorf("message = %+v", got)
	}
	if "<"+got.MessageID+">" != msg.MessageID {
		t.Errorf("Message-ID = %q, want %q", got.MessageID, msg.MessageID)
	}
	if headers["X-Goerp-Type"] == nil && headers["X-GoERP-Type"] == nil {
		t.Errorf("X-GoERP-Type missing from %v", headers)
	}
}

func TestResend_TransportFailuresPreserveCause(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Length", "100")
		_, _ = io.WriteString(w, `{"id":"truncated`)
	}))
	t.Cleanup(srv.Close)
	sender := &Resend{APIKey: "test-key", BaseURL: srv.URL}

	_, err := sender.Send(t.Context(), testMessage("test@example.test"))
	if !errors.Is(err, ErrConnection) || !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("truncated HTTP response = %v, want a connection failure wrapping unexpected EOF", err)
	}

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err = sender.Send(ctx, testMessage("test@example.test"))
	if !errors.Is(err, ErrConnection) || !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled HTTP request = %v, want a connection failure wrapping cancellation", err)
	}
}

func TestSMTP_RequiresSTARTTLSWhenUseTLS(t *testing.T) {
	s := &SMTP{Host: mailpitSMTPHost, Port: mailpitSMTPPort, UseTLS: true, AllowPrivateHosts: true}
	_, err := s.Send(t.Context(), testMessage("tls@example.test"))
	if err != nil && strings.Contains(err.Error(), "connection refused") {
		t.Skipf("mailpit SMTP not reachable: %v", err)
	}
	if !errors.Is(err, ErrPermanent) {
		t.Errorf("Send() over a server without STARTTLS = %v, want ErrPermanent", err)
	}
}
