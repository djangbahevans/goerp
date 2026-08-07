package schema

import "testing"

func TestShouldSkipSync(t *testing.T) {
	tests := []struct {
		name              string
		currentVersion    string
		lastSyncedVersion string
		wantSkip          bool
	}{
		{"matching versions skip", "1.0.0", "1.0.0", true},
		{"differing versions do not skip", "1.1.0", "1.0.0", false},
		{"empty last-synced version does not skip", "1.0.0", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := shouldSkipSync(tt.currentVersion, tt.lastSyncedVersion)
			if got != tt.wantSkip {
				t.Errorf("shouldSkipSync(%q, %q) = %v, want %v", tt.currentVersion, tt.lastSyncedVersion, got, tt.wantSkip)
			}
		})
	}
}
