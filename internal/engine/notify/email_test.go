package notify

import (
	"crypto/rand"
	"crypto/rsa"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/auth/signingkey"
	"github.com/djangbahevans/goerp/internal/engine/jobqueue"
	"github.com/djangbahevans/goerp/internal/engine/manifest"
	"github.com/djangbahevans/goerp/internal/engine/module"
	"github.com/djangbahevans/goerp/internal/engine/notifconfig"
	"github.com/djangbahevans/goerp/internal/engine/notifications"
	"github.com/djangbahevans/goerp/internal/engine/notiftemplate"
	"github.com/djangbahevans/goerp/internal/engine/registry"
	"github.com/djangbahevans/goerp/internal/engine/tenant"
	"github.com/djangbahevans/goerp/internal/engine/tenantschema"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
)

const mailpitAPI = "http://localhost:8025/api/v1"

var emailTypes = []manifest.NotificationType{
	{
		Name:              "order_confirmed",
		Label:             "Order Confirmed",
		DefaultChannels:   []string{inApp, email},
		AvailableChannels: []string{inApp, email},
		Templates: map[string]string{
			inApp: "notifications/order_confirmed/in_app.{locale}.json",
			email: "notifications/order_confirmed/email.{locale}.html",
		},
	},
}

func writeFiles(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for rel, content := range files {
		path := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// emailRegistry holds a "sales" module with email subject, HTML and text
// templates, and an "acme_theme" theme module with an email layout.
func emailRegistry(t *testing.T) *registry.ModuleRegistry {
	t.Helper()
	sales := t.TempDir()
	writeFiles(t, sales, map[string]string{
		"notifications/order_confirmed/in_app.en.json": `{"title": "Order {{.OrderReference}} confirmed", "action_url": "/_m/sales/orders/{{.OrderID}}"}`,
		"notifications/order_confirmed/email.en.json":  `{"subject": "Order {{.OrderReference}} Confirmed — {{.TenantName}}"}`,
		"notifications/order_confirmed/email.en.html":  `<h1>Order {{.OrderReference}}</h1><p>Dear {{.UserFirstName}},</p><a href="{{.ActionURL}}">View Order</a>`,
		"notifications/order_confirmed/email.en.txt":   "Order {{.OrderReference}} confirmed.\nView: {{.ActionURL}}\n",
	})
	templates, err := notiftemplate.Load(emailTypes, sales)
	if err != nil {
		t.Fatalf("notiftemplate.Load() error: %v", err)
	}
	theme := t.TempDir()
	writeFiles(t, theme, map[string]string{
		"emails/layout.html": `<html><body><div id="acme-layout">{{.TenantName}}|{{.Content}}|{{.UnsubscribeURL}}|{{.Year}}</div></body></html>`,
	})

	reg := &registry.ModuleRegistry{}
	if _, err := reg.Update(map[string]*module.LoadedModule{
		"sales": {
			Status:         module.StatusReady,
			Manifest:       manifest.Manifest{Name: "sales", Type: "standard", Version: "1.0.0", NotificationTypes: emailTypes},
			NotifTemplates: templates,
		},
		"acme_theme": {
			Status:      module.StatusReady,
			Manifest:    manifest.Manifest{Name: "acme_theme", Type: "theme", Version: "1.0.0"},
			PackagePath: theme,
		},
	}); err != nil {
		t.Fatalf("registry Update() error: %v", err)
	}
	return reg
}

type emailEnv struct {
	*testEnv
	worker *EmailWorker
	codec  *notifications.UnsubscribeCodec
	resend *resendStub
}

func openEmailEnv(t *testing.T) *emailEnv {
	t.Helper()
	env := openTestEnv(t)
	reg := emailRegistry(t)
	env.sender.Registry = reg
	env.seedTemplates(t, reg)
	*env.config = notifconfig.Config{
		EmailEnabled: true,
		Email: notifconfig.EmailConfig{
			Provider: notifconfig.ProviderSMTP, FromName: "Acme Corp", FromAddr: "noreply@acme.test", ReplyTo: "support@acme.test",
			APIKey: "re_test",
			SMTP:   notifconfig.SMTPConfig{Host: "localhost", Port: 1025},
		},
	}

	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	codec := notifications.NewUnsubscribeCodec(&signingkey.SigningKeySet{
		Active: signingkey.SigningKey{KID: "k1", Algorithm: "RS256", Private: priv, Public: &priv.PublicKey},
	})
	resend := newResendStub(t)
	worker := NewEmailWorker(EmailDeps{
		DB:                    env.conn,
		Registry:              reg,
		Config:                staticConfig{env.config},
		Tenants:               tenant.NewStore(env.conn),
		Unsubscribe:           codec,
		AppBaseURL:            "http://localhost:8080",
		PlatformDomain:        "goerp.local",
		ResendBaseURL:         resend.srv.URL,
		SMTPAllowPrivateHosts: true,
	})
	return &emailEnv{testEnv: env, worker: worker, codec: codec, resend: resend}
}

// send sends an order_confirmed notification to a new user and returns
// the user's address and its email_send job's args.
func (e *emailEnv) send(t *testing.T) (string, jobqueue.EmailSendArgs) {
	t.Helper()
	userID := e.createUser(t, "Ama Owusu", "")
	res, err := e.sender.Send(t.Context(), e.tenant.ID, "sales", orderConfirmed, userID,
		map[string]any{"OrderReference": "ORD-42", "OrderID": "o-42"}, Options{})
	if err != nil {
		t.Fatalf("Send() error: %v", err)
	}
	var raw []byte
	if err := e.conn.QueryRow(`SELECT args FROM system.river_job WHERE kind = 'email_send' AND args->>'notification_id' = $1`, res.NotificationID).Scan(&raw); err != nil {
		t.Fatalf("load email_send job: %v", err)
	}
	var args jobqueue.EmailSendArgs
	if err := json.Unmarshal(raw, &args); err != nil {
		t.Fatal(err)
	}
	return args.Recipient, args
}

func (e *emailEnv) work(t *testing.T, args jobqueue.EmailSendArgs, attempt int) error {
	t.Helper()
	return e.worker.Work(t.Context(), &river.Job[jobqueue.EmailSendArgs]{
		JobRow: &rivertype.JobRow{Attempt: attempt, MaxAttempts: jobqueue.NotificationDeliveryMaxAttempts},
		Args:   args,
	})
}

type emailRow struct {
	status, provider, providerID, reason string
	attempts                             int
	delivered                            bool
}

func (e *emailEnv) row(t *testing.T, deliveryID string) emailRow {
	t.Helper()
	var r emailRow
	err := e.conn.QueryRow(fmt.Sprintf(`
		SELECT status, COALESCE(provider, ''), COALESCE(provider_id, ''), COALESCE(failure_reason, ''), attempt_count, delivered_at IS NOT NULL
		FROM %s.notification_deliveries WHERE id = $1
	`, tenantschema.Name(e.tenant.Slug)), deliveryID).Scan(&r.status, &r.provider, &r.providerID, &r.reason, &r.attempts, &r.delivered)
	if err != nil {
		t.Fatalf("load delivery: %v", err)
	}
	return r
}

// resendStub is a Resend API that honors Idempotency-Key the way Resend
// does: a repeated key with the same body returns the first response
// without sending again, and with a different body is a 409.
type resendStub struct {
	srv    *httptest.Server
	mu     sync.Mutex
	status int
	sent   []map[string]any
	keys   map[string]string
	bodies map[string]string
}

func newResendStub(t *testing.T) *resendStub {
	s := &resendStub{status: http.StatusOK, keys: map[string]string{}, bodies: map[string]string{}}
	s.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.status != http.StatusOK {
			w.WriteHeader(s.status)
			_, _ = io.WriteString(w, `{"name":"application_error","message":"stub failure"}`)
			return
		}
		key := r.Header.Get("Idempotency-Key")
		if id, ok := s.keys[key]; ok {
			if s.bodies[key] != string(body) {
				w.WriteHeader(http.StatusConflict)
				_, _ = io.WriteString(w, `{"name":"invalid_idempotent_request","message":"payload differs"}`)
				return
			}
			_, _ = fmt.Fprintf(w, `{"id":%q}`, id)
			return
		}
		var req map[string]any
		_ = json.Unmarshal(body, &req)
		s.sent = append(s.sent, req)
		id := fmt.Sprintf("re_%d", len(s.sent))
		s.keys[key], s.bodies[key] = id, string(body)
		_, _ = fmt.Fprintf(w, `{"id":%q}`, id)
	}))
	t.Cleanup(s.srv.Close)
	return s
}

func (s *resendStub) setStatus(status int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.status = status
}

func (s *resendStub) sentCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.sent)
}

type mailpitMessage struct {
	MessageID string `json:"MessageID"`
	Subject   string `json:"Subject"`
	HTML      string `json:"HTML"`
	Text      string `json:"Text"`
}

func mailpitGet(t *testing.T, u string, out any) {
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

// mailpitMessages returns every message Mailpit holds for to, with its
// headers.
func mailpitMessages(t *testing.T, to string) ([]mailpitMessage, []map[string][]string) {
	t.Helper()
	var found struct {
		Messages []struct {
			ID string `json:"ID"`
		} `json:"messages"`
	}
	mailpitGet(t, mailpitAPI+"/search?query="+url.QueryEscape("to:"+to), &found)
	var msgs []mailpitMessage
	var headers []map[string][]string
	for _, m := range found.Messages {
		var msg mailpitMessage
		mailpitGet(t, mailpitAPI+"/message/"+m.ID, &msg)
		h := map[string][]string{}
		mailpitGet(t, mailpitAPI+"/message/"+m.ID+"/headers", &h)
		msgs = append(msgs, msg)
		headers = append(headers, h)
	}
	return msgs, headers
}

func header(h map[string][]string, name string) string {
	for k, v := range h {
		if strings.EqualFold(k, name) && len(v) > 0 {
			return v[0]
		}
	}
	return ""
}

func TestEmailWorker_DeliversRenderedEmailThroughSMTP(t *testing.T) {
	env := openEmailEnv(t)
	to, args := env.send(t)

	if err := env.work(t, args, 1); err != nil {
		t.Fatalf("Work() error: %v", err)
	}

	msgs, headers := mailpitMessages(t, to)
	if len(msgs) != 1 {
		t.Fatalf("%d messages to %s in mailpit, want 1", len(msgs), to)
	}
	msg, h := msgs[0], headers[0]
	if msg.Subject != "Order ORD-42 Confirmed — Notify Test Co" {
		t.Errorf("Subject = %q", msg.Subject)
	}
	base := "http://" + env.tenant.Slug + ".goerp.local:8080"
	if !strings.Contains(msg.HTML, "<h1>Order ORD-42</h1><p>Dear Ama,</p>") ||
		!strings.Contains(msg.HTML, `href="`+base+`/_m/sales/orders/o-42"`) ||
		!strings.Contains(msg.HTML, "Unsubscribe from these emails") {
		t.Errorf("HTML is not the rendered template in the default layout:\n%s", msg.HTML)
	}
	if !strings.Contains(strings.ReplaceAll(msg.Text, "\r\n", "\n"), "Order ORD-42 confirmed.\nView: "+base+"/_m/sales/orders/o-42") {
		t.Errorf("Text = %q", msg.Text)
	}

	unsubscribe := header(h, "List-Unsubscribe")
	if !strings.HasPrefix(unsubscribe, "<"+base+"/_notif/unsubscribe?token=") {
		t.Fatalf("List-Unsubscribe = %q", unsubscribe)
	}
	u, _ := url.Parse(strings.Trim(unsubscribe, "<>"))
	claims, err := env.codec.Verify(u.Query().Get("token"))
	if err != nil || claims.NotificationType != orderConfirmed || claims.TenantID != env.tenant.ID {
		t.Errorf("unsubscribe token = %+v, %v", claims, err)
	}
	if got := header(h, "List-Unsubscribe-Post"); got != "List-Unsubscribe=One-Click" {
		t.Errorf("List-Unsubscribe-Post = %q", got)
	}
	if want := "<" + args.NotificationID + "@acme.test>"; "<"+msg.MessageID+">" != want {
		t.Errorf("Message-ID = %q, want %s", msg.MessageID, want)
	}
	if header(h, "X-GoERP-Tenant") != env.tenant.Slug || header(h, "X-GoERP-Type") != orderConfirmed {
		t.Errorf("X-GoERP headers = %q / %q", header(h, "X-GoERP-Tenant"), header(h, "X-GoERP-Type"))
	}

	row := env.row(t, args.DeliveryID)
	if row.status != notifications.DeliveryDelivered || !row.delivered || row.provider != notifconfig.ProviderSMTP ||
		row.providerID != "<"+args.NotificationID+"@acme.test>" || row.attempts != 1 {
		t.Errorf("delivery row = %+v", row)
	}
}

func TestEmailWorker_BlockedSMTPDestinationFailsPermanently(t *testing.T) {
	env := openEmailEnv(t)
	env.worker.SMTPAllowPrivateHosts = false
	_, args := env.send(t)

	err := env.work(t, args, 1)
	if _, ok := errors.AsType[*river.JobCancelError](err); !ok {
		t.Fatalf("Work() = %v, want permanent cancellation for a blocked SMTP destination", err)
	}

	row := env.row(t, args.DeliveryID)
	if row.status != notifications.DeliveryFailed || row.attempts != 1 || row.delivered || !strings.Contains(row.reason, "not a public address") {
		t.Fatalf("blocked SMTP delivery = %+v, want failed on the first attempt", row)
	}
}

func TestEmailWorker_ProviderSwitchAppliesToTheNextSend(t *testing.T) {
	env := openEmailEnv(t)
	_, first := env.send(t)
	if err := env.work(t, first, 1); err != nil {
		t.Fatalf("Work() error: %v", err)
	}
	if env.resend.sentCount() != 0 {
		t.Fatalf("resend used while the provider is smtp")
	}

	env.config.Email.Provider = notifconfig.ProviderResend
	_, second := env.send(t)
	if err := env.work(t, second, 1); err != nil {
		t.Fatalf("Work() error: %v", err)
	}
	if env.resend.sentCount() != 1 {
		t.Fatalf("resend sent %d emails, want 1", env.resend.sentCount())
	}
	if r := env.row(t, first.DeliveryID); r.provider != notifconfig.ProviderSMTP {
		t.Errorf("first delivery provider = %q", r.provider)
	}
	if r := env.row(t, second.DeliveryID); r.provider != notifconfig.ProviderResend || r.providerID != "re_1" || r.status != notifications.DeliveryDelivered {
		t.Errorf("second delivery = %+v", r)
	}
}

func TestEmailWorker_RetryAfterASucceededSendDoesNotSendTwice(t *testing.T) {
	env := openEmailEnv(t)
	env.config.Email.Provider = notifconfig.ProviderResend
	_, args := env.send(t)

	if err := env.work(t, args, 1); err != nil {
		t.Fatalf("Work() error: %v", err)
	}

	// The job retried after its row was updated: nothing is sent.
	if err := env.work(t, args, 2); err != nil {
		t.Fatalf("retried Work() error: %v", err)
	}
	if n := env.resend.sentCount(); n != 1 {
		t.Fatalf("resend sent %d emails after a retry of a delivered row, want 1", n)
	}

	// The job retried after the provider accepted the send but before its
	// row was updated: the same idempotency key and an identical body let
	// the provider answer with the first send.
	if _, err := env.conn.Exec(fmt.Sprintf(`UPDATE %s.notification_deliveries SET status = 'pending', provider_id = NULL WHERE id = $1`,
		tenantschema.Name(env.tenant.Slug)), args.DeliveryID); err != nil {
		t.Fatal(err)
	}
	if err := env.work(t, args, 2); err != nil {
		t.Fatalf("Work() after a crash error: %v", err)
	}
	if n := env.resend.sentCount(); n != 1 {
		t.Errorf("resend sent %d emails, want 1", n)
	}
	if r := env.row(t, args.DeliveryID); r.status != notifications.DeliveryDelivered || r.providerID != "re_1" {
		t.Errorf("delivery row = %+v", r)
	}
}

func TestEmailWorker_RetryWithAChangedBodyAfterASucceededSendIsDelivered(t *testing.T) {
	env := openEmailEnv(t)
	env.config.Email.Provider = notifconfig.ProviderResend
	_, args := env.send(t)
	if err := env.work(t, args, 1); err != nil {
		t.Fatalf("Work() error: %v", err)
	}
	if _, err := env.conn.Exec(fmt.Sprintf(`UPDATE %s.notification_deliveries SET status = 'retrying', provider_id = NULL WHERE id = $1`,
		tenantschema.Name(env.tenant.Slug)), args.DeliveryID); err != nil {
		t.Fatal(err)
	}
	env.config.Email.FromName = "Acme Renamed"

	if err := env.work(t, args, 2); err != nil {
		t.Fatalf("Work() error: %v", err)
	}
	if n := env.resend.sentCount(); n != 1 {
		t.Errorf("resend sent %d emails, want 1", n)
	}
	if r := env.row(t, args.DeliveryID); r.status != notifications.DeliveryDelivered || r.providerID != "" {
		t.Errorf("delivery row = %+v, want delivered with no provider id", r)
	}
}

func TestEmailWorker_FailsAfterFiveAttempts(t *testing.T) {
	env := openEmailEnv(t)
	env.config.Email.Provider = notifconfig.ProviderResend
	env.resend.setStatus(http.StatusInternalServerError)
	_, args := env.send(t)

	for attempt := 1; attempt < jobqueue.NotificationDeliveryMaxAttempts; attempt++ {
		err := env.work(t, args, attempt)
		if _, canceled := errors.AsType[*river.JobCancelError](err); err == nil || canceled {
			t.Fatalf("attempt %d: Work() = %v, want a retryable error", attempt, err)
		}
		if r := env.row(t, args.DeliveryID); r.status != notifications.DeliveryRetrying || r.attempts != attempt || r.reason == "" {
			t.Fatalf("attempt %d: delivery row = %+v", attempt, r)
		}
	}

	err := env.work(t, args, jobqueue.NotificationDeliveryMaxAttempts)
	if _, ok := errors.AsType[*river.JobCancelError](err); !ok {
		t.Fatalf("last attempt: Work() = %v, want a JobCancel", err)
	}
	if r := env.row(t, args.DeliveryID); r.status != notifications.DeliveryFailed || r.attempts != 5 || r.delivered {
		t.Errorf("delivery row = %+v", r)
	}
}

func TestEmailWorker_QuotaRefusalIsQuotaExceeded(t *testing.T) {
	env := openEmailEnv(t)
	env.config.Email.Provider = notifconfig.ProviderResend
	env.resend.setStatus(http.StatusTooManyRequests)
	_, args := env.send(t)

	if err := env.work(t, args, 1); err == nil {
		t.Fatal("Work() error = nil")
	}
	if r := env.row(t, args.DeliveryID); r.status != notifications.DeliveryQuotaExceeded {
		t.Errorf("delivery row = %+v", r)
	}
}

func TestEmailWorker_PermanentFailureFailsAtOnce(t *testing.T) {
	env := openEmailEnv(t)
	env.config.Email.Provider = notifconfig.ProviderResend
	env.resend.setStatus(http.StatusUnprocessableEntity)
	_, args := env.send(t)

	err := env.work(t, args, 1)
	if _, ok := errors.AsType[*river.JobCancelError](err); !ok {
		t.Fatalf("Work() = %v, want a JobCancel", err)
	}
	if r := env.row(t, args.DeliveryID); r.status != notifications.DeliveryFailed || r.attempts != 1 {
		t.Errorf("delivery row = %+v", r)
	}
}

func TestEmailWorker_UsesTheTenantsThemeLayout(t *testing.T) {
	env := openEmailEnv(t)
	env.config.Email.LayoutTemplate = "acme_theme/emails/layout.html"
	to, args := env.send(t)

	if err := env.work(t, args, 1); err != nil {
		t.Fatalf("Work() error: %v", err)
	}
	msgs, _ := mailpitMessages(t, to)
	if len(msgs) != 1 {
		t.Fatalf("%d messages, want 1", len(msgs))
	}
	html := msgs[0].HTML
	if !strings.Contains(html, `<div id="acme-layout">Notify Test Co|<h1>Order ORD-42</h1>`) ||
		!strings.Contains(html, "/_notif/unsubscribe?token=") || !strings.Contains(html, fmt.Sprintf("|%d</div>", time.Now().Year())) {
		t.Errorf("HTML is not in the theme layout:\n%s", html)
	}
}

func TestEmailWorker_UnknownLayoutFailsPermanently(t *testing.T) {
	env := openEmailEnv(t)
	env.config.Email.LayoutTemplate = "sales/emails/layout.html"
	_, args := env.send(t)

	err := env.work(t, args, 1)
	if _, ok := errors.AsType[*river.JobCancelError](err); !ok || !strings.Contains(err.Error(), "not an installed theme module") {
		t.Fatalf("Work() = %v, want a JobCancel naming the non-theme module", err)
	}
}
