package orm

import (
	"testing"
	"time"
)

func TestResolvePeriodKey(t *testing.T) {
	at := time.Date(2026, time.March, 5, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name   string
		format string
		want   string
	}{
		{"no tokens", "INV", "INV"},
		{"year only", "{year}", "2026"},
		{"month only", "{month}", "03"},
		{"day only", "{day}", "05"},
		{"multiple tokens", "INV-{year}-{month}-{day}", "INV-2026-03-05"},
		{"repeated token", "{year}/{year}", "2026/2026"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := ResolvePeriodKey(tc.format, at); got != tc.want {
				t.Errorf("ResolvePeriodKey(%q, %v) = %q, want %q", tc.format, at, got, tc.want)
			}
		})
	}
}

func TestResolvePeriodKey_SingleDigitMonthDay(t *testing.T) {
	at := time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)
	got := ResolvePeriodKey("{year}-{month}-{day}", at)
	want := "2026-01-01"
	if got != want {
		t.Errorf("ResolvePeriodKey = %q, want %q", got, want)
	}
}
