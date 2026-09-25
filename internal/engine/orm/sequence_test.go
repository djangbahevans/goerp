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

func TestFormatSequence(t *testing.T) {
	at := time.Date(2026, time.March, 5, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name   string
		format string
		n      int64
		want   string
	}{
		{"padded seq", "TK-{seq:04}", 1, "TK-0001"},
		{"year and padded seq", "INV/{year}/{seq:05}", 123, "INV/2026/00123"},
		{"all period tokens", "{year}{month}{day}-{seq:03}", 7, "20260305-007"},
		{"seq wider than its padding", "{seq:02}", 12345, "12345"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := FormatSequence(tc.format, at, tc.n); got != tc.want {
				t.Errorf("FormatSequence(%q, %v, %d) = %q, want %q", tc.format, at, tc.n, got, tc.want)
			}
		})
	}
}

func TestValidateSequenceFormat(t *testing.T) {
	for _, format := range []string{"TK-{seq:04}", "INV/{year}/{seq:05}", "{year}{month}-{seq:3}"} {
		if err := ValidateSequenceFormat(format); err != nil {
			t.Errorf("ValidateSequenceFormat(%q) = %v, want nil", format, err)
		}
	}
	for _, format := range []string{"{year}", "INV-{year}-", "{seq:03}-{seq:03}", "N{seq}", "{day}-{seq:03}", "{Year}-{seq:03}", "{seq:0}", "{seq:21}", "{seq:1000000000}"} {
		if err := ValidateSequenceFormat(format); err == nil {
			t.Errorf("ValidateSequenceFormat(%q) = nil, want an error", format)
		}
	}
}

// A {year} format's period key changes with the year, so the counter
// AcquireNext keys on it restarts at 1 in the new year.
func TestFormatSequence_YearTokenFollowsTheAcquisitionYear(t *testing.T) {
	format := "INV/{year}/{seq:05}"
	dec := time.Date(2026, time.December, 31, 23, 0, 0, 0, time.UTC)
	jan := time.Date(2027, time.January, 1, 1, 0, 0, 0, time.UTC)

	if ResolvePeriodKey(format, dec) == ResolvePeriodKey(format, jan) {
		t.Fatal("period key is the same across years, want a new counter period in the new year")
	}
	if got := FormatSequence(format, jan, 1); got != "INV/2027/00001" {
		t.Errorf("first number of 2027 = %q, want INV/2027/00001", got)
	}
}
