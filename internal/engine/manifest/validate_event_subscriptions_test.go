package manifest

import (
	"encoding/json/v2"
	"fmt"
	"strings"
	"testing"
)

func manifestWithSubscribes(t *testing.T, subs []map[string]any) []byte {
	t.Helper()
	fields := minimalManifestFields()
	fields["subscribes"] = subs
	m, err := json.Marshal(fields)
	if err != nil {
		t.Fatalf("marshal fixture: %v", err)
	}
	return m
}

func TestLoadManifest_SubscribesDuplicateNameAndVersion_Rejected(t *testing.T) {
	for name, subs := range map[string][]map[string]any{
		"explicit": {
			{"name": "sale.order.confirmed", "version": 2, "async": true},
			{"name": "sale.order.confirmed", "version": 2, "async": true},
		},
		"omitted version equals explicit 1": {
			{"name": "sale.order.confirmed", "async": true},
			{"name": "sale.order.confirmed", "version": 1, "async": true},
		},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := Load(manifestWithSubscribes(t, subs))
			if err == nil || !strings.Contains(err.Error(), `duplicate subscription to event "sale.order.confirmed"`) {
				t.Fatalf("err = %v, want a duplicate-subscription error naming the event", err)
			}
		})
	}
}

func TestLoadManifest_SubscribesSameNameDifferentVersions_Passes(t *testing.T) {
	_, err := Load(manifestWithSubscribes(t, []map[string]any{
		{"name": "sale.order.confirmed", "version": 1, "async": true},
		{"name": "sale.order.confirmed", "version": 2, "async": true},
	}))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
}

func TestLoadManifest_SystemSubscriptions(t *testing.T) {
	for _, async := range []bool{false, true} {
		t.Run(fmt.Sprintf("async=%v", async), func(t *testing.T) {
			m, err := Load(manifestWithSubscribes(t, []map[string]any{{"name": "system.user.created", "handler": "handle_created", "async": async}}))
			if !async {
				if err == nil || !strings.Contains(err.Error(), "requires async: true") {
					t.Fatalf("load error = %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got := m.Subscribes[0].EffectiveVersion(); got != 0 {
				t.Fatalf("system subscription version = %d, want 0", got)
			}
		})
	}
}
