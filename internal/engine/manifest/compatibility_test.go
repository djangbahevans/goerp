package manifest

import "testing"

func TestEngineVersionCompatibility(t *testing.T) {
	for _, tc := range []struct {
		rangeText, version string
		wantError          bool
	}{
		{">=0.5.0 <1.0.0", "0.5.0", false},
		{">=0.5.0 <1.0.0", "1.0.0", true},
		{"^1.0.0", "1.2.0", false},
		{">=99.0.0", "dev", false},
		{"invalid", "dev", true},
		{"*", "broken", true},
	} {
		if err := ValidateEngineVersion(Manifest{Engine: tc.rangeText}, tc.version); (err != nil) != tc.wantError {
			t.Fatalf("range=%q version=%q error=%v", tc.rangeText, tc.version, err)
		}
	}
}
