package notify

import (
	"fmt"
	"testing"

	"github.com/djangbahevans/goerp/internal/engine/notifications"
	"github.com/djangbahevans/goerp/internal/engine/providerselect"
	"github.com/djangbahevans/goerp/internal/engine/tenantschema"
)

func TestProviderDelivery_RealConnectorCorrectsOneTokenDuringSuccessfulAttempt(t *testing.T) {
	env := openTestEnv(t)
	env.useInstalledProviders(t, map[string]string{pushConnector: providerselect.CategoryPush})
	worker := env.connectorWorker(t, pushConnector, providerselect.CategoryPush)
	userID := env.createUser(t, "Ama", "")
	for _, token := range []string{"tok-a", "tok-b", "tok-c"} {
		env.registerDevice(t, userID, "android", token)
	}

	env.sendParked(t, userID, map[string]any{"OrderReference": "correct"}, Options{})
	job := env.providerJob(t, JobTypePushSend, 1, 5)
	if err := worker.Work(t.Context(), job); err != nil {
		t.Fatalf("Work: %v", err)
	}

	got := env.channelDeliveries(t, push)
	if len(got) != 3 {
		t.Fatalf("deliveries = %+v, want three recipients", got)
	}
	for _, row := range got {
		wantStatus, wantReason := notifications.DeliveryAccepted, ""
		if row.recipient == "tok-b" {
			wantStatus, wantReason = notifications.DeliveryFailed, "unregistered token"
		}
		if row.status != wantStatus || row.reason != wantReason {
			t.Errorf("%s: status=%s reason=%q, want %s %q", row.recipient, row.status, row.reason, wantStatus, wantReason)
		}
	}
	query := fmt.Sprintf("SELECT count(*) FROM %s.user_device_tokens WHERE token = $1", tenantschema.Name(env.tenant.Slug))
	if n := env.count(t, query, "tok-b"); n != 0 {
		t.Errorf("invalid token has %d registrations, want none", n)
	}
	if n := env.count(t, query, "tok-a"); n != 1 {
		t.Errorf("valid token has %d registrations, want one", n)
	}

	if err := worker.Work(t.Context(), job); err != nil {
		t.Fatalf("repeat Work: %v", err)
	}
	if got := env.connectorObservations(t); len(got) != 1 {
		t.Errorf("connector ran %d times, want one with every delivery final", len(got))
	}
}
