package schema

import "testing"

func TestParseSchedule(t *testing.T) {
	cases := []struct {
		in      string
		wantErr bool
	}{
		{"", false},
		{"2026-05-09T02:00:00Z", false},
		{"2026-05-09T02:00:00+03:00", false},
		{"2026-05-09T02:00:00", true},
		{"not-a-timestamp", true},
	}
	for _, c := range cases {
		err := parseSchedule(c.in)
		if c.wantErr && err == nil {
			t.Errorf("parseSchedule(%q) error = nil, want an error", c.in)
		}
		if !c.wantErr && err != nil {
			t.Errorf("parseSchedule(%q) error: %v", c.in, err)
		}
	}
}
