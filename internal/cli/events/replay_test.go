package events

import (
	"testing"
	"time"
)

func TestParseTimeOrRelative(t *testing.T) {
	now := time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)

	cases := []struct {
		in      string
		want    time.Time
		wantErr bool
	}{
		{"2h", now.Add(-2 * time.Hour), false},
		{"7d", now.Add(-7 * 24 * time.Hour), false},
		{"0d", now, false},
		{"2026-05-01T00:00:00Z", time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC), false},
		{"not-a-time", time.Time{}, true},
		{"", time.Time{}, true},
	}
	for _, c := range cases {
		got, err := parseTimeOrRelative(c.in, now)
		if c.wantErr {
			if err == nil {
				t.Errorf("parseTimeOrRelative(%q) error = nil, want an error", c.in)
			}
			continue
		}
		if err != nil {
			t.Errorf("parseTimeOrRelative(%q) error: %v", c.in, err)
			continue
		}
		if !got.Equal(c.want) {
			t.Errorf("parseTimeOrRelative(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}
