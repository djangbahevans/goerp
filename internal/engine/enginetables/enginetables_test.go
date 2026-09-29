package enginetables

import "testing"

func TestIsEngineOwned_NotificationTables(t *testing.T) {
	for _, name := range []string{"notifications", "notification_deliveries", "notification_preferences", "user_device_tokens"} {
		if !IsEngineOwned(name) {
			t.Errorf("IsEngineOwned(%q) = false, want true", name)
		}
	}
	if IsEngineOwned("notification_deliveries_archive") {
		t.Error("IsEngineOwned(notification_deliveries_archive) = true for a non-partitioned table's lookalike")
	}
}
