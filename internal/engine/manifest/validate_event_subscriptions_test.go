package manifest

import (
	"encoding/json"
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
