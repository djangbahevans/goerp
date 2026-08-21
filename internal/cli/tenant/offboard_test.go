package tenant

import (
	"testing"
	"time"
)

func TestParseGracePeriod(t *testing.T) {
	cases := []struct {
		in      string
		want    time.Duration
		wantErr bool
	}{
		{"30d", 30 * 24 * time.Hour, false},
		{"0d", 0, false},
		{"7d", 7 * 24 * time.Hour, false},
		{"720h", 720 * time.Hour, false},
		{"not-a-duration", 0, true},
		{"", 0, true},
	}
	for _, c := range cases {
		got, err := parseGracePeriod(c.in)
		if c.wantErr {
			if err == nil {
				t.Errorf("parseGracePeriod(%q) error = nil, want an error", c.in)
			}
			continue
		}
		if err != nil {
			t.Errorf("parseGracePeriod(%q) error: %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("parseGracePeriod(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}
