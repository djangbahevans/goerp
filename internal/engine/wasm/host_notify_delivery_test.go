package wasm

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"
	"uuid"

	abiv1 "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/djangbahevans/goerp/internal/engine/abi"
	"github.com/djangbahevans/goerp/internal/engine/notifications"
	"github.com/djangbahevans/goerp/internal/engine/tenantschema"
)

type notifyDeliveryEnv struct {
	db   *sql.DB
	rt   *Runtime
	mc   *ModuleContext
	inst *ModuleInstance
	id   string
}

func newNotifyDeliveryEnv(t *testing.T) *notifyDeliveryEnv {
	t.Helper()
	conn := openTestPrimaryDB(t)
	rt := newHostDBTestRuntime(t, conn, 10)
	mc := newNotifyTestModuleContext(abi.CapNotifyManageDeliveries)
	mc.TenantSlug = fmt.Sprintf("notifydelivery%d", time.Now().UnixNano())
	schema := tenantschema.Name(mc.TenantSlug)
	if _, err := conn.ExecContext(t.Context(), "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = conn.Exec("DROP SCHEMA " + schema + " CASCADE") })

	store := notifications.NewStore(conn)
	for _, bootstrap := range []func(context.Context, string) error{store.BootstrapFeed, store.BootstrapDeliveries, store.BootstrapDeviceTokens} {
		if err := bootstrap(t.Context(), mc.TenantSlug); err != nil {
			t.Fatal(err)
		}
	}
	id := uuid.New().String()
	if _, err := conn.ExecContext(t.Context(), fmt.Sprintf(`
		INSERT INTO %s.notifications (id, tenant_id, user_id, type, module, title)
		VALUES ($1, $2, $3, 'sales.order_confirmed', 'sales', 'Test')
	`, schema), id, mc.TenantID, uuid.New().String()); err != nil {
		t.Fatal(err)
	}
	for _, recipient := range []string{"tok-a", "tok-b"} {
		if _, err := conn.ExecContext(t.Context(), fmt.Sprintf(`
			INSERT INTO %s.notification_deliveries (tenant_id, notification_id, channel, recipient, status, idempotency_key)
			VALUES ($1, $2, 'push', $3, 'pending', $3)
		`, schema), mc.TenantID, id, recipient); err != nil {
			t.Fatal(err)
		}
	}

	return &notifyDeliveryEnv{
		db:   conn,
		rt:   rt,
		mc:   mc,
		inst: newHostNotifyCaller(t, t.Context(), rt, mc),
		id:   id,
	}
}

func (e *notifyDeliveryEnv) report() abiv1.NotifyUpdateDeliveryStatusInput {
	return abiv1.NotifyUpdateDeliveryStatusInput{
		NotificationID: e.id,
		Channel:        "push",
		Recipient:      "tok-a",
		Status:         "failed",
		Reason:         "invalid token",
	}
}

func assertNotifyError(t *testing.T, env abiv1.Envelope, code string) {
	t.Helper()
	if env.OK || env.Error == nil || env.Error.Code != code || env.Error.Retry {
		t.Fatalf("response = %+v, want non-retryable %s", env, code)
	}
}

func TestHostNotify_DeliveryCorrectionRequiresManageDeliveries(t *testing.T) {
	env := newNotifyDeliveryEnv(t)
	for _, caps := range []abi.CapabilitySet{0, abi.CapNotifySend} {
		mc := newNotifyTestModuleContext(caps)
		mc.TenantID, mc.TenantSlug = env.mc.TenantID, env.mc.TenantSlug
		env.inst.SetModuleContext(mc)
		assertNotifyError(t, callHost(t, t.Context(), env.inst, "call_remove_device_token", abiv1.NotifyRemoveDeviceTokenInput{Token: "tok-a"}), abiv1.ErrCodeCapabilityDenied)
		assertNotifyError(t, callHost(t, t.Context(), env.inst, "call_update_delivery_status", env.report()), abiv1.ErrCodeCapabilityDenied)
	}

	var status string
	if err := env.db.QueryRowContext(t.Context(), fmt.Sprintf("SELECT status FROM %s.notification_deliveries WHERE recipient = 'tok-a'", tenantschema.Name(env.mc.TenantSlug))).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "pending" {
		t.Fatalf("unauthorized report changed status to %s", status)
	}
}

func TestHostNotify_RemoveDeviceTokenIsTenantScopedAndIdempotent(t *testing.T) {
	env := newNotifyDeliveryEnv(t)
	schema := tenantschema.Name(env.mc.TenantSlug)
	otherTenant := uuid.New().String()
	for _, tenantID := range []string{env.mc.TenantID, otherTenant} {
		for range 2 {
			if _, err := env.db.ExecContext(t.Context(), fmt.Sprintf(`
				INSERT INTO %s.user_device_tokens (tenant_id, user_id, platform, token) VALUES ($1, $2, 'android', 'tok-a')
			`, schema), tenantID, uuid.New().String()); err != nil {
				t.Fatal(err)
			}
		}
	}

	for range 2 {
		if got := callHost(t, t.Context(), env.inst, "call_remove_device_token", abiv1.NotifyRemoveDeviceTokenInput{Token: "tok-a"}); !got.OK {
			t.Fatalf("remove token: %+v", got.Error)
		}
	}

	var count int
	if err := env.db.QueryRowContext(t.Context(), fmt.Sprintf("SELECT count(*) FROM %s.user_device_tokens WHERE tenant_id = $1", schema), otherTenant).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("other tenant has %d tokens, want two", count)
	}
	if err := env.db.QueryRowContext(t.Context(), fmt.Sprintf("SELECT count(*) FROM %s.user_device_tokens WHERE tenant_id = $1", schema), env.mc.TenantID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("calling tenant has %d tokens, want none", count)
	}
}

func TestHostNotify_UpdateDeliveryStatusUpdatesOneRecipient(t *testing.T) {
	env := newNotifyDeliveryEnv(t)
	var deliveredAt time.Time
	for _, status := range []string{"accepted", "quota_exceeded", "failed", "delivered", "delivered", "failed"} {
		in := env.report()
		in.NotificationID = "urn:uuid:" + env.id
		in.Status = status
		if status == "delivered" {
			in.Reason = ""
		}
		if got := callHost(t, t.Context(), env.inst, "call_update_delivery_status", in); !got.OK {
			t.Fatalf("report %s: %+v", status, got.Error)
		}

		var gotStatus string
		var reason sql.NullString
		var delivered sql.NullTime
		if err := env.db.QueryRowContext(t.Context(), fmt.Sprintf(`
			SELECT status, failure_reason, delivered_at FROM %s.notification_deliveries WHERE recipient = 'tok-a'
		`, tenantschema.Name(env.mc.TenantSlug))).Scan(&gotStatus, &reason, &delivered); err != nil {
			t.Fatal(err)
		}
		if gotStatus != status || reason.String != in.Reason || delivered.Valid != (status == "delivered") {
			t.Fatalf("report %s: status=%s reason=%+v delivered=%+v", status, gotStatus, reason, delivered)
		}
		if delivered.Valid {
			if !deliveredAt.IsZero() && !deliveredAt.Equal(delivered.Time) {
				t.Fatalf("repeated report moved delivered_at from %s to %s", deliveredAt, delivered.Time)
			}
			deliveredAt = delivered.Time
		}
	}

	var status string
	if err := env.db.QueryRowContext(t.Context(), fmt.Sprintf("SELECT status FROM %s.notification_deliveries WHERE recipient = 'tok-b'", tenantschema.Name(env.mc.TenantSlug))).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "pending" {
		t.Fatalf("other recipient status = %s, want pending", status)
	}

	if _, err := env.db.ExecContext(t.Context(), fmt.Sprintf(`
		INSERT INTO %s.notification_deliveries (tenant_id, notification_id, channel, recipient, status, idempotency_key)
		VALUES ($1, $2, 'sms', 'tok-a', 'pending', 'sms:tok-a')
	`, tenantschema.Name(env.mc.TenantSlug)), env.mc.TenantID, env.id); err != nil {
		t.Fatal(err)
	}
	in := env.report()
	in.Channel, in.Status, in.Reason = "sms", "delivered", ""
	if got := callHost(t, t.Context(), env.inst, "call_update_delivery_status", in); !got.OK {
		t.Fatalf("SMS report: %+v", got.Error)
	}

	for channel, want := range map[string]string{"sms": "delivered", "push": "failed"} {
		if err := env.db.QueryRowContext(t.Context(), fmt.Sprintf(`
			SELECT status FROM %s.notification_deliveries WHERE recipient = 'tok-a' AND channel = $1
		`, tenantschema.Name(env.mc.TenantSlug)), channel).Scan(&status); err != nil {
			t.Fatal(err)
		}
		if status != want {
			t.Fatalf("%s status = %s, want %s", channel, status, want)
		}
	}
}

func TestHostNotify_UpdateDeliveryStatusRejectsMissingAndInvalidReports(t *testing.T) {
	env := newNotifyDeliveryEnv(t)
	for _, tc := range []struct {
		name, field, value, code string
	}{
		{"missing notification", "id", uuid.New().String(), abiv1.ErrCodeNotifyDeliveryNotFound},
		{"malformed notification", "id", "invalid", abiv1.ErrCodeNotifyDeliveryNotFound},
		{"missing recipient", "recipient", "tok-missing", abiv1.ErrCodeNotifyDeliveryNotFound},
		{"missing channel", "channel", "sms", abiv1.ErrCodeNotifyDeliveryNotFound},
		{"invalid channel", "channel", "email", abiv1.ErrCodeNotifyInvalidOptions},
		{"invalid status", "status", "pending", abiv1.ErrCodeNotifyInvalidOptions},
		{"empty recipient", "recipient", "", abiv1.ErrCodeNotifyInvalidOptions},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := env.report()
			switch tc.field {
			case "id":
				in.NotificationID = tc.value
			case "recipient":
				in.Recipient = tc.value
			case "channel":
				in.Channel = tc.value
			case "status":
				in.Status = tc.value
			}
			assertNotifyError(t, callHost(t, t.Context(), env.inst, "call_update_delivery_status", in), tc.code)
		})
	}

	mc := newNotifyTestModuleContext(abi.CapNotifyManageDeliveries)
	mc.TenantSlug = env.mc.TenantSlug
	env.inst.SetModuleContext(mc)
	assertNotifyError(t, callHost(t, t.Context(), env.inst, "call_update_delivery_status", env.report()), abiv1.ErrCodeNotifyDeliveryNotFound)
}

func TestHostNotify_DeliveryCorrectionRejectsUndecodableInput(t *testing.T) {
	env := newNotifyDeliveryEnv(t)
	for _, export := range []string{"call_remove_device_token", "call_update_delivery_status"} {
		assertNotifyError(t, callHost(t, t.Context(), env.inst, export, "not a request"), abiv1.ErrCodeDeserializeError)
	}
}

func TestHostNotify_DeliveryCorrectionWithoutDatabaseIsUnavailable(t *testing.T) {
	env := newNotifyDeliveryEnv(t)
	env.rt.primaryDB = nil
	for _, export := range []string{"call_remove_device_token", "call_update_delivery_status"} {
		got := callHost(t, t.Context(), env.inst, export, env.report())
		if got.OK || got.Error == nil || got.Error.Code != abiv1.ErrCodeUnavailable || !got.Error.Retry {
			t.Fatalf("%s: %+v, want retryable unavailable", export, got)
		}
	}
}
