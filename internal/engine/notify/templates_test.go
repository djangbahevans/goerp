package notify

import (
	"encoding/json/v2"
	"errors"
	"fmt"
	"strings"
	"testing"

	abiv1 "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/djangbahevans/goerp/internal/engine/jobqueue"
	"github.com/djangbahevans/goerp/internal/engine/notiftemplate"
	"github.com/djangbahevans/goerp/internal/engine/tenantschema"
	"github.com/vmihailenco/msgpack/v5"
)

func TestSendTemplates_EachPartFallsBackThroughTheLocalesOnItsOwn(t *testing.T) {
	st := sendTemplates{
		email: {
			{Locale: "fr", Fields: map[string]string{notiftemplate.ColHTML: "fr html"}},
			{Locale: "en", Fields: map[string]string{notiftemplate.ColHTML: "en html", notiftemplate.ColSubject: "en subject"}},
		},
		inApp: {
			{Locale: "fr", Fields: map[string]string{notiftemplate.ColTitle: "fr title"}},
			{Locale: "en", Fields: map[string]string{notiftemplate.ColTitle: "en title", notiftemplate.ColBody: "en body"}},
		},
	}
	if f, ok := st.part(email, notiftemplate.ColHTML); !ok || f[notiftemplate.ColHTML] != "fr html" {
		t.Errorf("html part = %v, %v, want the fr row", f, ok)
	}
	if f, ok := st.part(email, notiftemplate.ColSubject); !ok || f[notiftemplate.ColSubject] != "en subject" {
		t.Errorf("subject part = %v, %v, want the en row", f, ok)
	}
	if f, ok := st.part(inApp, inAppColumns...); !ok || f[notiftemplate.ColTitle] != "fr title" || f[notiftemplate.ColBody] != "" {
		t.Errorf("in_app part = %v, %v, want the whole fr row", f, ok)
	}
	if _, ok := st.part(email, notiftemplate.ColText); ok {
		t.Error("text part ok with no row defining text_template")
	}
	if _, ok := st.part(sms, notiftemplate.ColSMS); ok {
		t.Error("sms part ok with no sms rows")
	}
}

func (e *testEnv) saveOverride(t *testing.T, templateKey, channel, locale string, fields map[string]string) {
	t.Helper()
	r := notiftemplate.Row{TemplateKey: templateKey, Channel: channel, Locale: locale, Fields: fields}
	if err := e.store.SaveTemplateOverride(t.Context(), e.tenant.Slug, r); err != nil {
		t.Fatalf("SaveTemplateOverride(%s %s %s) error: %v", templateKey, channel, locale, err)
	}
}

func (e *testEnv) resetOverride(t *testing.T, templateKey, channel, locale string) {
	t.Helper()
	if err := e.store.ResetTemplate(t.Context(), e.tenant.Slug, templateKey, channel, locale); err != nil {
		t.Fatalf("ResetTemplate(%s %s %s) error: %v", templateKey, channel, locale, err)
	}
}

func (e *testEnv) notificationTitleBody(t *testing.T, notificationID string) (string, string) {
	t.Helper()
	var title, body string
	if err := e.conn.QueryRow(fmt.Sprintf(`SELECT title, COALESCE(body, '') FROM %s.notifications WHERE id = $1`, tenantschema.Name(e.tenant.Slug)), notificationID).Scan(&title, &body); err != nil {
		t.Fatalf("load notification: %v", err)
	}
	return title, body
}

// providerPayload decodes the payload of the jobType job enqueued for
// notificationID into out.
func (e *testEnv) providerPayload(t *testing.T, notificationID, jobType string, out any) {
	t.Helper()
	var raw []byte
	if err := e.conn.QueryRow(`SELECT args FROM system.river_job WHERE kind = 'wasm_job' AND args->>'notification_id' = $1 AND args->>'job_type' = $2`, notificationID, jobType).Scan(&raw); err != nil {
		t.Fatalf("load %s job: %v", jobType, err)
	}
	var args jobqueue.WASMJobArgs
	if err := json.Unmarshal(raw, &args); err != nil {
		t.Fatal(err)
	}
	if err := msgpack.Unmarshal(args.Payload, out); err != nil {
		t.Fatalf("decode %s payload: %v", jobType, err)
	}
}

func TestSend_TenantOverrideAppliesToInAppSMSAndPushUntilReset(t *testing.T) {
	env := openTestEnv(t)
	userID := env.createUser(t, "Kofi Mensah", "+233501234567")
	env.registerDevice(t, userID, "android", "tok-a")
	data := map[string]any{"OrderReference": "ORD-9"}
	opts := Options{ForceChannels: []string{sms}}

	env.saveOverride(t, orderConfirmed, inApp, "en", map[string]string{notiftemplate.ColTitle: "Custom {{.OrderReference}}", notiftemplate.ColBody: "Custom body"})
	env.saveOverride(t, orderConfirmed, sms, "en", map[string]string{notiftemplate.ColSMS: "Custom SMS {{.OrderReference}}"})
	env.saveOverride(t, orderConfirmed, push, "en", map[string]string{notiftemplate.ColPushTitle: "Custom push {{.OrderReference}}", notiftemplate.ColPushBody: "Custom push body"})

	type content struct{ title, body, sms, pushTitle, pushBody string }
	sendContent := func() content {
		res := env.sendParked(t, userID, data, opts)
		var c content
		c.title, c.body = env.notificationTitleBody(t, res.NotificationID)
		var smsPayload abiv1.SMSSendPayload
		env.providerPayload(t, res.NotificationID, JobTypeSMSSend, &smsPayload)
		var pushPayload abiv1.PushSendPayload
		env.providerPayload(t, res.NotificationID, JobTypePushSend, &pushPayload)
		c.sms, c.pushTitle, c.pushBody = smsPayload.Body, pushPayload.Title, pushPayload.Body
		return c
	}

	if got, want := sendContent(), (content{"Custom ORD-9", "Custom body", "Custom SMS ORD-9", "Custom push ORD-9", "Custom push body"}); got != want {
		t.Errorf("with overrides = %+v, want %+v", got, want)
	}

	env.resetOverride(t, orderConfirmed, inApp, "en")
	env.resetOverride(t, orderConfirmed, sms, "en")
	env.resetOverride(t, orderConfirmed, push, "en")
	want := content{"Order ORD-9 confirmed", "Hi Kofi, Notify Test Co confirmed it.", "Notify Test Co: order ORD-9 confirmed.", "Order ORD-9", "Confirmed by Notify Test Co"}
	if got := sendContent(); got != want {
		t.Errorf("after reset = %+v, want the shipped defaults %+v", got, want)
	}
}

func TestSend_TenantOverrideAppliesToEngineTypes(t *testing.T) {
	env := openTestEnv(t)
	userID := env.createUser(t, "Adwoa", "")
	key := EngineModule + ".activity_due"
	data := map[string]any{"Summary": "Call back", "TypeLabel": "Call", "TypeIcon": "phone", "RecordName": "SO-0007", "DueDate": "2026-09-25", "Overdue": false}
	send := func() (string, string) {
		res, err := env.sender.Send(t.Context(), env.tenant.ID, EngineModule, key, userID, data, Options{})
		if err != nil {
			t.Fatalf("Send() error: %v", err)
		}
		return env.notificationTitleBody(t, res.NotificationID)
	}

	env.saveOverride(t, key, inApp, "en", map[string]string{notiftemplate.ColTitle: "Today: {{.Summary}}"})
	if title, body := send(); title != "Today: Call back" || body != "" {
		t.Errorf("with override = (%q, %q), want the override's title and no body", title, body)
	}

	env.resetOverride(t, key, inApp, "en")
	if title, body := send(); title != "Due today: Call back" || body != "Call on SO-0007, due 2026-09-25" {
		t.Errorf("after reset = (%q, %q), want the embedded default", title, body)
	}
}

func TestSend_OverrideInARegionalLocaleOnlyReachesItsSpeakers(t *testing.T) {
	env := openTestEnv(t)
	french := env.createUser(t, "Aïcha", "")
	english := env.createUser(t, "Ama", "")
	if _, err := env.conn.Exec(`UPDATE system.user_profiles SET locale = 'fr-GH' WHERE user_id = $1`, french); err != nil {
		t.Fatal(err)
	}
	env.saveOverride(t, orderConfirmed, inApp, "fr", map[string]string{notiftemplate.ColTitle: "Commande {{.OrderReference}} confirmée"})

	results, err := env.sender.SendBulk(t.Context(), env.tenant.ID, "sales", orderConfirmed, []string{french, english}, map[string]any{"OrderReference": "ORD-3"}, Options{})
	if err != nil {
		t.Fatalf("SendBulk() error: %v", err)
	}
	if title, _ := env.notificationTitleBody(t, results[0].NotificationID); title != "Commande ORD-3 confirmée" {
		t.Errorf("fr-GH title = %q, want the fr override", title)
	}
	if title, _ := env.notificationTitleBody(t, results[1].NotificationID); title != "Order ORD-3 confirmed" {
		t.Errorf("en title = %q, want the en default", title)
	}
}

func TestSend_UnparseableOverrideFailsLikeAnUnrenderableTemplate(t *testing.T) {
	env := openTestEnv(t)
	userID := env.createUser(t, "Kofi", "")
	env.saveOverride(t, orderConfirmed, inApp, "en", map[string]string{notiftemplate.ColTitle: "{{.Unclosed"})

	_, err := env.sender.Send(t.Context(), env.tenant.ID, "sales", orderConfirmed, userID, nil, Options{})
	if !errors.Is(err, ErrRenderFailed) {
		t.Fatalf("Send() error = %v, want ErrRenderFailed", err)
	}
	if n := env.notificationCount(t); n != 0 {
		t.Errorf("notifications = %d, want 0", n)
	}
}

func TestEmailWorker_RendersTheTenantsOverrideUntilReset(t *testing.T) {
	env := openEmailEnv(t)
	env.saveOverride(t, orderConfirmed, email, "en", map[string]string{
		notiftemplate.ColSubject: "Custom subject {{.OrderReference}}",
		notiftemplate.ColHTML:    "<p>Custom {{.OrderReference}} &amp; {{.UserFirstName}}</p>",
		notiftemplate.ColText:    "Custom text {{.OrderReference}}",
	})
	deliver := func() mailpitMessage {
		to, args := env.send(t)
		if err := env.work(t, args, 1); err != nil {
			t.Fatalf("Work() error: %v", err)
		}
		msgs, _ := mailpitMessages(t, to)
		if len(msgs) != 1 {
			t.Fatalf("%d messages to %s, want 1", len(msgs), to)
		}
		return msgs[0]
	}

	msg := deliver()
	if msg.Subject != "Custom subject ORD-42" || !strings.Contains(msg.HTML, "<p>Custom ORD-42 &amp; Ama</p>") || !strings.Contains(msg.Text, "Custom text ORD-42") {
		t.Errorf("with override: subject %q\nhtml %s\ntext %q", msg.Subject, msg.HTML, msg.Text)
	}

	env.resetOverride(t, orderConfirmed, email, "en")
	msg = deliver()
	if msg.Subject != "Order ORD-42 Confirmed — Notify Test Co" || !strings.Contains(msg.HTML, "<h1>Order ORD-42</h1>") {
		t.Errorf("after reset: subject %q\nhtml %s, want the shipped default", msg.Subject, msg.HTML)
	}
}
