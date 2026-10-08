package tenantexport

import (
	"encoding/json/v2"
	"testing"
)

func TestExportRecordMarshal_MapKeysAreDeterministicallyOrdered(t *testing.T) {
	rec := exportRecord{
		Model: "widget",
		Record: map[string]any{
			"z": 1, "a": 2, "m": 3, "b": 4, "y": 5,
		},
	}

	first, err := json.Marshal(rec, json.Deterministic(true))
	if err != nil {
		t.Fatalf("Marshal() error: %v", err)
	}
	for range 20 {
		got, err := json.Marshal(rec, json.Deterministic(true))
		if err != nil {
			t.Fatalf("Marshal() error: %v", err)
		}
		if string(got) != string(first) {
			t.Fatalf("Marshal() = %s, want %s (same bytes every call)", got, first)
		}
	}
}
