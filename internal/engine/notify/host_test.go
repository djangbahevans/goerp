package notify

import (
	"errors"
	"fmt"
	"slices"
	"testing"

	abiv1 "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/djangbahevans/goerp/internal/engine/wasm"
)

func hostRequest(env *testEnv, notificationType string, userIDs ...string) wasm.NotifyRequest {
	return wasm.NotifyRequest{TenantID: env.tenant.ID, ModuleName: "sales", NotificationType: notificationType, UserIDs: userIDs, TraceID: "trace-1"}
}

func requireHostErrorCode(t *testing.T, err error, code string) {
	t.Helper()
	hostErr, ok := errors.AsType[*abiv1.HostError](err)
	if !ok || hostErr.Code != code {
		t.Fatalf("error = %v, want a %s HostError", err, code)
	}
}

func TestHostSender_RejectsAnotherModulesType(t *testing.T) {
	env := openTestEnv(t)
	userID := env.createUser(t, "Ama Owusu", "")
	host := HostSender{env.sender}

	for _, typ := range []string{"billing.invoice_overdue", "engine.activity_due", "order_confirmed", "sales.undeclared"} {
		_, err := host.SendBulk(t.Context(), hostRequest(env, typ, userID))
		requireHostErrorCode(t, err, abiv1.ErrCodeNotifyUndeclaredType)
	}
	if n := env.notificationCount(t); n != 0 {
		t.Errorf("notifications = %d, want 0", n)
	}
}

func TestHostSender_MapsCallerErrors(t *testing.T) {
	env := openTestEnv(t)
	userID := env.createUser(t, "Ama Owusu", "")
	host := HostSender{env.sender}

	outsider := hostRequest(env, orderConfirmed, env.createOutsider(t, "Yaw Boateng", ""))
	_, err := host.SendBulk(t.Context(), outsider)
	requireHostErrorCode(t, err, abiv1.ErrCodeNotifyUnknownRecipient)

	badPriority := hostRequest(env, orderConfirmed, userID)
	badPriority.Opts.Priority = "urgent"
	_, err = host.SendBulk(t.Context(), badPriority)
	requireHostErrorCode(t, err, abiv1.ErrCodeNotifyInvalidOptions)

	badChannel := hostRequest(env, orderConfirmed, userID)
	badChannel.Opts.ChannelOverride = "fax"
	_, err = host.SendBulk(t.Context(), badChannel)
	requireHostErrorCode(t, err, abiv1.ErrCodeNotifyInvalidOptions)

	// A map key is printed unescaped, so this data renders the in_app
	// template to invalid JSON.
	unrenderable := hostRequest(env, orderConfirmed, userID)
	unrenderable.Data = map[string]any{"OrderReference": map[string]any{`a"b`: 1}}
	_, err = host.SendBulk(t.Context(), unrenderable)
	requireHostErrorCode(t, err, abiv1.ErrCodeNotifyRenderFailed)

	userIDs :=make([]string, MaxBulkRecipients+1)
	for i := range userIDs {
		userIDs[i] = fmt.Sprintf("00000000-0000-0000-0000-%012d", i)
	}
	_, err = host.SendBulk(t.Context(), hostRequest(env, orderConfirmed, userIDs...))
	requireHostErrorCode(t, err, abiv1.ErrCodeNotifyTooManyRecipients)
}

func TestHostSender_AppliesOptions(t *testing.T) {
	env := openTestEnv(t)
	userID := env.createUser(t, "Ama Owusu", "+233200000000")
	host := HostSender{env.sender}

	req := hostRequest(env, orderConfirmed, userID)
	req.Opts = abiv1.NotifySendOptions{ChannelOverride: sms, ActionURL: "/_m/sales/orders/o-9", IdempotencyKey: "o-9"}
	results, err := host.SendBulk(t.Context(), req)
	if err != nil {
		t.Fatalf("SendBulk() error: %v", err)
	}
	if len(results) != 1 || results[0].UserID != userID || !slices.Contains(results[0].ChannelsUsed, sms) {
		t.Fatalf("SendBulk() = %+v, want one result for %s with sms forced", results, userID)
	}

	var actionURL string
	if err := env.conn.QueryRow(fmt.Sprintf(`SELECT action_url FROM tenant_%s.notifications WHERE id = $1`, env.tenant.Slug), results[0].NotificationID).Scan(&actionURL); err != nil {
		t.Fatal(err)
	}
	if actionURL != "/_m/sales/orders/o-9" {
		t.Errorf("action_url = %q, want the WithActionURL override", actionURL)
	}

	again, err := host.SendBulk(t.Context(), req)
	if err != nil {
		t.Fatalf("repeated SendBulk() error: %v", err)
	}
	if !again[0].Deduplicated || again[0].NotificationID != results[0].NotificationID {
		t.Errorf("repeated SendBulk() = %+v, want the first notification deduplicated", again)
	}
}

func TestHostSender_SendTxAnnouncesOnlyWhenAsked(t *testing.T) {
	env := openTestEnv(t)
	userID := env.createUser(t, "Ama Owusu", "")
	host := HostSender{env.sender}

	tx, err := env.conn.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	res, announce, err := host.SendTx(t.Context(), tx, hostRequest(env, orderConfirmed, userID))
	if err != nil {
		t.Fatalf("SendTx() error: %v", err)
	}
	if len(env.hub.sent) != 0 {
		t.Fatal("SendTx() pushed notification.new before its transaction committed")
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	announce(t.Context())
	if len(env.hub.sent) != 1 || env.hub.sent[0].userID != userID {
		t.Fatalf("pushes after announce = %+v, want one to %s", env.hub.sent, userID)
	}
	if res.NotificationID == "" || res.ChannelsUsed[0] != inApp {
		t.Errorf("SendTx() = %+v", res)
	}
}

func TestHostSender_SendTxRollbackLeavesNothing(t *testing.T) {
	env := openTestEnv(t)
	userID := env.createUser(t, "Ama Owusu", "")
	host := HostSender{env.sender}

	tx, err := env.conn.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := host.SendTx(t.Context(), tx, hostRequest(env, orderConfirmed, userID)); err != nil {
		t.Fatalf("SendTx() error: %v", err)
	}
	_ = tx.Rollback()
	if env.notificationCount(t) != 0 || env.deliveryCount(t) != 0 || env.jobCount(t) != 0 {
		t.Error("a rolled-back SendTx() left a notification, delivery or job behind")
	}
}
