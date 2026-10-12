package manifest

import (
	"encoding/json/v2"
	"testing"
)

func TestRetentionPreviewDefault(t *testing.T) {
	for _, tc := range []struct {
		name  string
		value *bool
		want  bool
	}{
		{name: "omitted", want: true},
		{name: "allowed", value: new(true), want: true},
		{name: "disallowed", value: new(false), want: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fields := minimalManifestFields()
			policy := map[string]any{"table": "activities", "retain_days": 30, "action": "delete"}
			if tc.value != nil {
				policy["dry_run_safe"] = *tc.value
			}
			fields["retention_policies"] = []any{policy}

			data, err := json.Marshal(fields)
			if err != nil {
				t.Fatal(err)
			}

			m, err := Load(data)
			if err != nil {
				t.Fatal(err)
			}
			if got := m.RetentionPolicies[0].IsDryRunSafe(); got != tc.want {
				t.Errorf("IsDryRunSafe = %v, want %v", got, tc.want)
			}

			data, err = json.Marshal(m)
			if err != nil {
				t.Fatal(err)
			}

			var roundTrip Manifest
			if err := json.Unmarshal(data, &roundTrip); err != nil {
				t.Fatal(err)
			}
			if got := roundTrip.RetentionPolicies[0].IsDryRunSafe(); got != tc.want {
				t.Errorf("round-trip IsDryRunSafe = %v, want %v", got, tc.want)
			}
		})
	}
}
