package modeltest

import (
	"net/http"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
	"uuid"

	"github.com/djangbahevans/goerp/internal/engine/notiftemplate"
	"github.com/djangbahevans/goerp/internal/engine/role"
	fixture "github.com/djangbahevans/goerp/sdk/go/modeltest/testdata/notificationsfixture"
	"github.com/djangbahevans/goerp/sdk/go/notify"
)

func notificationHarness(t *testing.T) *Harness {
	t.Helper()
	t.Chdir("testdata/notificationsfixture")
	h := NewHarness(t, WithClock(func() time.Time { return time.Date(2040, 1, 2, 0, 0, 0, 0, time.UTC) }))
	h.DB.SeedSystem("user_profiles", map[string]any{"user_id": h.UserID, "name": "Ama Mensah", "locale": "en"})
	for key, value := range map[string]any{
		"notifications.email.provider":  "smtp",
		"notifications.email.smtp.host": "localhost",
	} {
		h.DB.SeedSystem("tenant_config_overrides", map[string]any{
			"tenant_id": h.TenantID, "key": "engine." + key, "value": value,
		})
	}

	return h
}

func sendNotification(t *testing.T, h *Harness, reference string, opts ...QueryOption) *Response {
	t.Helper()
	opts = append(opts, WithQuery("reference", reference))
	return h.POST("/notifprobe/send", nil, opts...)
}

func TestNotifications_AssertionsAndLastReadCommittedModuleNotifications(t *testing.T) {
	h := notificationHarness(t)
	n := *h.Notifications
	n.t = panicFailer{t}

	h.Notifications.AssertNotSent(fixture.Confirmed)
	if msg := failure(func() { n.AssertSent(fixture.Confirmed) }); !strings.Contains(msg, "notifprobe.confirmed") {
		t.Fatalf("unsent assertion failure = %q", msg)
	}
	if msg := failure(func() { n.Last(fixture.Confirmed) }); !strings.Contains(msg, "notifprobe.confirmed") {
		t.Fatalf("missing Last failure = %q", msg)
	}

	for _, reference := range []string{"ORD-1", "ORD-2"} {
		response := sendNotification(t, h, reference)
		if response.StatusCode != http.StatusOK {
			t.Fatalf("send: status=%d body=%s", response.StatusCode, response.body)
		}
	}
	h.Notifications.AssertSent(fixture.Confirmed)
	h.Notifications.AssertSentN(fixture.Confirmed, 2)
	h.Notifications.AssertNotSent(fixture.Other)
	if msg := failure(func() { n.AssertSent(fixture.Other) }); !strings.Contains(msg, "notifprobe.other") {
		t.Errorf("other definition failure = %q", msg)
	}
	if msg := failure(func() { n.AssertNotSent(fixture.Confirmed) }); msg == "" {
		t.Error("AssertNotSent accepted sent notifications")
	}
	if msg := failure(func() { n.AssertSentN(fixture.Confirmed, 1) }); msg == "" {
		t.Error("AssertSentN accepted incorrect count")
	}

	last := h.Notifications.Last(fixture.Confirmed)
	if last.UserID != h.UserID || last.Title != "Order ORD-2 confirmed" || last.ActionURL != "/_m/notifprobe/orders/ORD-2" {
		t.Errorf("Last = %+v", last)
	}
	if last.Body != "Kwame Mensah: 9007199254740993" {
		t.Errorf("body = %q", last.Body)
	}

	h.Notifications.AssertSentViaInApp(fixture.Confirmed)
	h.Notifications.AssertSentViaEmail(fixture.Confirmed)
	h.Notifications.AssertNotSentViaSMS(fixture.Confirmed)
	h.Notifications.AssertNotSentViaPush(fixture.Confirmed)
	for name, assertion := range map[string]func(){
		"not in-app":  func() { n.AssertNotSentViaInApp(fixture.Confirmed) },
		"not email":   func() { n.AssertNotSentViaEmail(fixture.Confirmed) },
		"SMS":         func() { n.AssertSentViaSMS(fixture.Confirmed) },
		"push":        func() { n.AssertSentViaPush(fixture.Confirmed) },
		"other email": func() { n.AssertSentViaEmail(fixture.Other) },
	} {
		if msg := failure(assertion); msg == "" {
			t.Errorf("%s assertion accepted wrong deliveries", name)
		}
	}

	h.DB.Seed("notification_preferences", map[string]any{
		"user_id": h.UserID, "notification_type": "notifprobe.confirmed", "email_enabled": false,
	})
	if response := sendNotification(t, h, "ORD-3"); response.StatusCode != http.StatusOK {
		t.Fatalf("send after opt-out: %d %s", response.StatusCode, response.body)
	}
	last = h.Notifications.Last(fixture.Confirmed)
	h.DB.AssertCount("notification_deliveries", 0, "notification_id = '"+last.ID+"' AND channel = 'email'")
}

func TestNotifications_TransactionsBulkAndIdempotency(t *testing.T) {
	h := notificationHarness(t)
	if response := sendNotification(t, h, "rollback", WithQuery("mode", "rollback")); response.StatusCode != http.StatusInternalServerError {
		t.Fatalf("rollback: %d %s", response.StatusCode, response.body)
	}
	h.Notifications.AssertNotSent(fixture.Confirmed)
	h.DB.AssertCount("notification_deliveries", 0, "1=1")

	if response := sendNotification(t, h, "transaction", WithQuery("mode", "tx")); response.StatusCode != http.StatusOK {
		t.Fatalf("transaction: %d %s", response.StatusCode, response.body)
	}
	h.Notifications.AssertSentN(fixture.Confirmed, 1)

	otherID := uuid.New().String()
	h.DB.SeedSystem("users", map[string]any{"id": otherID, "email": otherID + "@modeltest.invalid"})
	t.Cleanup(func() { _, _ = h.DB.db.Exec("DELETE FROM system.users WHERE id = $1", otherID) })
	h.DB.insertInto(h.DB.schema(), "tenant_members", map[string]any{"user_id": otherID})
	roles := role.NewStore(h.DB.db)
	roleID, err := roles.GetRoleByName(t.Context(), h.tenantSlug, "user")
	if err != nil {
		t.Fatal(err)
	}
	if err := roles.AssignRole(t.Context(), h.tenantSlug, otherID, roleID, ""); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		response := sendNotification(t, h, "bulk", WithQuery("mode", "bulk"), WithQuery("recipient", otherID), WithQuery("key", "bulk-key"))
		if response.StatusCode != http.StatusOK {
			t.Fatalf("bulk: %d %s", response.StatusCode, response.body)
		}
	}
	h.Notifications.AssertSentN(fixture.Confirmed, 3)
	if last := h.Notifications.Last(fixture.Confirmed); last.UserID != otherID || last.Title != "Order bulk confirmed" {
		t.Errorf("bulk Last = %+v", last)
	}

	response := sendNotification(t, h, "invalid", WithQuery("mode", "bulk"), WithQuery("recipient", uuid.New().String()))
	if response.StatusCode != http.StatusInternalServerError {
		t.Fatalf("invalid bulk: %d %s", response.StatusCode, response.body)
	}
	h.Notifications.AssertSentN(fixture.Confirmed, 3)
}

func TestNotifications_AllChannelAssertionsUseDeliveryRows(t *testing.T) {
	h := notificationHarness(t)
	if _, err := h.DB.db.ExecContext(t.Context(), "UPDATE "+h.DB.schema()+".tenant_members SET phone = '+233201234567' WHERE user_id = $1", h.UserID); err != nil {
		t.Fatal(err)
	}
	for _, category := range []string{"sms_provider", "push_provider"} {
		h.DB.SeedSystem("tenant_module_settings", map[string]any{
			"tenant_id": h.TenantID, "module_name": category,
		})
		h.DB.SeedSystem("tenant_module_provider_categories", map[string]any{
			"tenant_id": h.TenantID, "module_name": category, "category": category,
		})
	}
	for _, token := range []string{"first-device", "second-device"} {
		h.DB.Seed("user_device_tokens", map[string]any{
			"user_id": h.UserID, "platform": "web", "token": token,
		})
	}

	response := sendNotification(t, h, "channels", WithQuery("mode", "channels"))
	if response.StatusCode != http.StatusOK {
		t.Fatalf("send: %d %s", response.StatusCode, response.body)
	}
	if _, err := h.DB.db.ExecContext(t.Context(), "UPDATE "+h.DB.schema()+".notification_deliveries SET status = 'failed'"); err != nil {
		t.Fatal(err)
	}
	h.Notifications.AssertSentN(fixture.Confirmed, 1)
	h.Notifications.AssertSentViaSMS(fixture.Confirmed)
	h.Notifications.AssertSentViaPush(fixture.Confirmed)
	h.Notifications.AssertNotSentViaInApp(fixture.Other)
	h.Notifications.AssertNotSentViaEmail(fixture.Other)
	h.Notifications.AssertNotSentViaSMS(fixture.Other)
	h.Notifications.AssertNotSentViaPush(fixture.Other)

	n := *h.Notifications
	n.t = panicFailer{t}
	for name, assertion := range map[string]func(){
		"not SMS":      func() { n.AssertNotSentViaSMS(fixture.Confirmed) },
		"not push":     func() { n.AssertNotSentViaPush(fixture.Confirmed) },
		"other in-app": func() { n.AssertSentViaInApp(fixture.Other) },
	} {
		if msg := failure(assertion); msg == "" {
			t.Errorf("%s assertion accepted wrong deliveries", name)
		}
	}
}

func TestNotifications_RenderTemplateTypedDataLocaleFallbackAndErrors(t *testing.T) {
	h := notificationHarness(t)
	data := fixture.Data{Reference: "ORD-42", Customer: "<Kwame>", Amount: 9007199254740993}
	rendered, err := h.Notifications.RenderTemplate(fixture.Confirmed, notify.ChannelEmail, "en", data)
	if err != nil {
		t.Fatal(err)
	}
	if rendered.Subject != "Confirmed ORD-42" || !strings.Contains(rendered.HTML, "&lt;Kwame&gt;: ORD-42 9007199254740993 Module test tenant Ama") || !strings.Contains(rendered.Text, "<Kwame>: ORD-42") {
		t.Errorf("email = %+v", rendered)
	}

	rendered, err = h.Notifications.RenderTemplate(fixture.Confirmed, notify.ChannelEmail, "fr-GH", data)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(rendered.HTML, "Bonjour &lt;Kwame&gt;") || rendered.Subject != "Confirmed ORD-42" || !strings.Contains(rendered.Text, "ORD-42") {
		t.Errorf("regional fallback email = %+v", rendered)
	}
	rendered, err = h.Notifications.RenderTemplate(fixture.Confirmed, notify.ChannelInApp, "fr", data)
	if err != nil {
		t.Fatal(err)
	}
	if rendered.Title != "Commande ORD-42" || rendered.Body != "" || rendered.ActionURL != "" {
		t.Errorf("in-app JSON must use one locale variant: %+v", rendered)
	}
	for _, channel := range []string{notify.ChannelSMS, notify.ChannelPush, notify.ChannelInApp} {
		rendered, err := h.Notifications.RenderTemplate(fixture.Confirmed, channel, "de", data)
		if err != nil || !strings.Contains(rendered.Title+rendered.Body, "ORD-42") {
			t.Errorf("%s English fallback = %+v, %v", channel, rendered, err)
		}
	}

	h.DB.insertInto(h.DB.schema(), "notification_templates", map[string]any{
		"template_key": "notifprobe.confirmed", "channel": "email", "locale": "en", "subject_template": "Tenant override",
	})
	rendered, err = h.Notifications.RenderTemplate(fixture.Confirmed, notify.ChannelEmail, "en", data)
	if err != nil || rendered.Subject != "Confirmed ORD-42" {
		t.Fatalf("bundled render used tenant override: %+v, %v", rendered, err)
	}

	for name, render := range map[string]func() error{
		"missing template": func() error {
			_, err := h.Notifications.RenderTemplate(fixture.Other, "email", "en", data)
			return err
		},
		"unknown channel": func() error {
			_, err := h.Notifications.RenderTemplate(fixture.Confirmed, "fax", "en", data)
			return err
		},
		"zero definition": func() error {
			_, err := h.Notifications.RenderTemplate(notify.Def[fixture.Data]{}, "email", "en", data)
			return err
		},
		"execution": func() error {
			broken := data
			broken.Items = []string{"one"}
			_, err := h.Notifications.RenderTemplate(fixture.Confirmed, "email", "en", broken)
			return err
		},
	} {
		if err := render(); err == nil {
			t.Errorf("%s returned no error", name)
		}
	}

	n := *h.Notifications
	n.rows = slices.Clone(n.rows)
	n.rows = append(n.rows, notiftemplate.Row{TemplateKey: "notifprobe.other", Channel: "email", Locale: "en", Fields: map[string]string{notiftemplate.ColHTML: "{{"}})
	if _, err := n.RenderTemplate(fixture.Other, "email", "en", data); err == nil {
		t.Error("invalid template returned no error")
	}
}

func TestNotifications_RenderTemplateRejectsWrongDataTypeAtCompileTime(t *testing.T) {
	cmd := exec.CommandContext(t.Context(), "go", "build", "-o", filepath.Join(t.TempDir(), "wrongdata"), "./testdata/wrongnotificationdata")
	output, err := cmd.CombinedOutput()
	if err == nil || !strings.Contains(string(output), "cannot use") || !strings.Contains(string(output), "fixture.Data") {
		t.Fatalf("wrong data build: %v\n%s", err, output)
	}
}
