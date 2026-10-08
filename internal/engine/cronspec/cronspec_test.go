package cronspec

import (
	"fmt"
	"testing"
	"time"

	"github.com/djangbahevans/goerp/sdk/go/jobs/def"
)

func utc(value string) time.Time {
	t, err := time.Parse(time.DateTime, value)
	if err != nil {
		panic(err)
	}
	return t.UTC()
}

func TestNext(t *testing.T) {
	tests := []struct {
		expr  string
		after string
		want  string
	}{
		{"* * * * *", "2026-10-08 10:15:30", "2026-10-08 10:16:00"},
		{"0 3 * * 0", "2026-10-08 10:00:00", "2026-10-11 03:00:00"},
		{"0 9 1 * *", "2026-10-08 10:00:00", "2026-11-01 09:00:00"},
		{"30 8 * * 1-5", "2026-10-09 09:00:00", "2026-10-12 08:30:00"},
		{"0 */6 * * *", "2026-10-08 06:00:00", "2026-10-08 12:00:00"},
		{"0 0 1 1 *", "2026-10-08 10:00:00", "2027-01-01 00:00:00"},
		{"15,45 * * * *", "2026-10-08 10:20:00", "2026-10-08 10:45:00"},
		{"0-30/10 * * * *", "2026-10-08 10:25:00", "2026-10-08 10:30:00"},
		{"0-30/10 * * * *", "2026-10-08 10:30:00", "2026-10-08 11:00:00"},
		{"0 0 * jan mon-fri", "2026-10-08 10:00:00", "2027-01-01 00:00:00"},
		{"0 0 * * 7", "2026-10-08 10:00:00", "2026-10-11 00:00:00"},
		{"0 0 * * 5-7", "2026-10-08 10:00:00", "2026-10-09 00:00:00"},
		{"0 0 * * fri-7", "2026-10-10 10:00:00", "2026-10-11 00:00:00"},
		{"0 0 * * 1,7", "2026-10-11 10:00:00", "2026-10-12 00:00:00"},
		{"0 0 * * 1-7/2", "2026-10-08 10:00:00", "2026-10-09 00:00:00"},
		{"0 0 13 * fri", "2026-10-08 10:00:00", "2026-10-09 00:00:00"},
		{"0 0 13 * fri", "2026-10-09 10:00:00", "2026-10-13 00:00:00"},
		{"0  3   * * *", "2026-10-08 10:00:00", "2026-10-09 03:00:00"},
	}
	for _, tt := range tests {
		t.Run(tt.expr+" after "+tt.after, func(t *testing.T) {
			schedule, err := Parse(tt.expr)
			if err != nil {
				t.Fatalf("Parse(%q) = %v", tt.expr, err)
			}
			if got, want := schedule.Next(utc(tt.after)), utc(tt.want); !got.Equal(want) {
				t.Errorf("Next(%s) = %s, want %s", tt.after, got, want)
			}
		})
	}
}

func TestNextIsEvaluatedInUTC(t *testing.T) {
	schedule, err := Parse("0 3 * * *")
	if err != nil {
		t.Fatal(err)
	}
	accra := time.FixedZone("UTC+5", 5*60*60)
	after := time.Date(2026, 10, 8, 2, 0, 0, 0, accra) // 21:00 UTC the previous day

	got := schedule.Next(after)

	if want := utc("2026-10-08 03:00:00"); !got.Equal(want) || got.Location() != time.UTC {
		t.Errorf("Next = %s, want %s in UTC", got, want)
	}
}

func TestNextWithoutAFireTime(t *testing.T) {
	schedule, err := Parse("0 0 31 2 *")
	if err != nil {
		t.Fatal(err)
	}
	if got := schedule.Next(utc("2026-10-08 10:00:00")); !got.IsZero() {
		t.Errorf("Next = %s, want the zero time", got)
	}
}

// Every expression is also run through the definition-time validator in
// sdk/go/jobs/def so the two grammars cannot drift apart.
var corpus = []struct {
	expr  string
	valid bool
}{
	{"* * * * *", true},
	{"0 3 * * *", true},
	{"*/5 * * * *", true},
	{"0 0 1 1 *", true},
	{"0 9-17 * * 1-5", true},
	{"15,45 * * * *", true},
	{"0 0 * * 0", true},
	{"0 0 * * 7", true},
	{"0 0 * JAN MON-FRI", true},
	{"0 0 * jan mon-fri", true},
	{"0-30/10 * * * *", true},
	{"5/10 * * * *", true},
	{"0  3   * * *", true},
	{"0 0 31 2 *", true},
	{"0 0 * * 5-7", true},
	{"0 0 * * 6-7/2", true},
	{"", false},
	{"* * * *", false},
	{"* * * * * *", false},
	{"0 0 0 * * *", false},
	{"@daily", false},
	{"@every 1h", false},
	{"TZ=UTC 0 0 * * *", false},
	{"CRON_TZ=UTC 0 0 * * *", false},
	{"60 * * * *", false},
	{"* 24 * * *", false},
	{"* * 0 * *", false},
	{"* * 32 * *", false},
	{"* * * 13 *", false},
	{"* * * * 8", false},
	{"a * * * *", false},
	{"*/0 * * * *", false},
	{"*/x * * * *", false},
	{"5-1 * * * *", false},
	{"1- * * * *", false},
	{",1 * * * *", false},
	{"0 0 * foo *", false},
	{"+5 * * * *", false},
	{"*/+5 * * * *", false},
	{"-1 * * * *", false},
	{"0 0 * * 7-5", false},
	{"0 0 * * 0-8", false},
}

func definitionAccepts(t *testing.T, expr string) (accepted bool) {
	t.Helper()
	defer func() {
		if recover() != nil {
			accepted = false
		}
	}()
	name := fmt.Sprintf("corpus_%x", expr)
	def.DefineCron("c"+name[:min(len(name), 40)], def.Label("Corpus"), def.Schedule(expr))
	return true
}

func TestParseMatchesDefinitionValidator(t *testing.T) {
	for _, tt := range corpus {
		t.Run(tt.expr, func(t *testing.T) {
			_, err := Parse(tt.expr)
			if gotValid := err == nil; gotValid != tt.valid {
				t.Errorf("Parse(%q) error = %v, want valid = %v", tt.expr, err, tt.valid)
			}
			if got := definitionAccepts(t, tt.expr); got != tt.valid {
				t.Errorf("sdk/go/jobs/def accepts %q = %v, want %v", tt.expr, got, tt.valid)
			}
		})
	}
}
