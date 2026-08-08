package abi

import "testing"

func TestCapabilitySet_Has(t *testing.T) {
	cs := CapDBRead | CapEventEmit

	if !cs.Has(CapDBRead) {
		t.Error("expected CapDBRead to be present")
	}
	if !cs.Has(CapEventEmit) {
		t.Error("expected CapEventEmit to be present")
	}
	if cs.Has(CapDBWrite) {
		t.Error("expected CapDBWrite to be absent")
	}
}

func TestResolveCapabilities_Individual(t *testing.T) {
	tests := []struct {
		capability string
		want       CapabilitySet
	}{
		{"db.read", CapDBRead},
		{"db.write", CapDBWrite},
		{"db.notify", CapDBNotify},
		{"cache.read", CapCacheRead},
		{"cache.write", CapCacheWrite},
		{"event.emit", CapEventEmit},
		{"storage.read", CapStorageRead},
		{"storage.write", CapStorageWrite},
		{"http.fetch", CapHTTPFetch},
		{"jobs.enqueue", CapJobsEnqueue},
		{"notify.send", CapNotifySend},
		{"notify.manage_deliveries", CapNotifyManageDeliveries},
		{"webhooks.deliver", CapWebhooksDeliver},
		{"ui.push", CapUIPush},
		{"search.query", CapSearchQuery},
		{"search.index", CapSearchIndex},
		{"authz.check", CapAuthzCheck},
		{"workflow.start", CapWorkflowStart},
		{"analytics.query", CapAnalyticsQuery},
		{"analytics.insert", CapAnalyticsInsert},
		{"analytics.export", CapAnalyticsExport},
	}

	for _, tt := range tests {
		t.Run(tt.capability, func(t *testing.T) {
			got, err := ResolveCapabilities([]string{tt.capability})
			if err != nil {
				t.Fatalf("ResolveCapabilities(%q) returned error: %v", tt.capability, err)
			}
			if got != tt.want {
				t.Errorf("ResolveCapabilities(%q) = %v, want %v", tt.capability, got, tt.want)
			}
		})
	}
}

func TestResolveCapabilities_Combination(t *testing.T) {
	got, err := ResolveCapabilities([]string{"db.read", "cache.read", "authz.check"})
	if err != nil {
		t.Fatalf("ResolveCapabilities returned error: %v", err)
	}

	want := CapDBRead | CapCacheRead | CapAuthzCheck
	if got != want {
		t.Errorf("ResolveCapabilities = %v, want %v", got, want)
	}

	if !got.Has(CapDBRead) || !got.Has(CapCacheRead) || !got.Has(CapAuthzCheck) {
		t.Error("expected all three declared capabilities to be present")
	}
	if got.Has(CapDBWrite) {
		t.Error("expected CapDBWrite to be absent from an undeclared combination")
	}
}

func TestResolveCapabilities_Unknown(t *testing.T) {
	_, err := ResolveCapabilities([]string{"db.read", "not.a.real.capability"})
	if err == nil {
		t.Fatal("expected an error for an unrecognized capability string")
	}
}

func TestResolveCapabilities_Empty(t *testing.T) {
	got, err := ResolveCapabilities(nil)
	if err != nil {
		t.Fatalf("ResolveCapabilities(nil) returned error: %v", err)
	}
	if got != 0 {
		t.Errorf("ResolveCapabilities(nil) = %v, want 0", got)
	}
}
