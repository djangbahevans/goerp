package jobqueue

import (
	"testing"
	"time"
)

func TestMinuteSchedule_NextIsTheFollowingUTCMinuteBoundary(t *testing.T) {
	tests := []struct{ from, want string }{
		{"2026-10-08 10:00:00", "2026-10-08 10:01:00"},
		{"2026-10-08 10:00:30", "2026-10-08 10:01:00"},
		{"2026-10-08 10:59:59", "2026-10-08 11:00:00"},
		{"2026-10-08 23:59:01", "2026-10-09 00:00:00"},
	}
	for _, tt := range tests {
		from, err := time.Parse(time.DateTime, tt.from)
		if err != nil {
			t.Fatal(err)
		}
		want, err := time.Parse(time.DateTime, tt.want)
		if err != nil {
			t.Fatal(err)
		}
		if got := (minuteSchedule{}).Next(from); !got.Equal(want) {
			t.Errorf("Next(%s) = %s, want %s", tt.from, got, want)
		}
	}
}

func TestMinuteSchedule_NextIsInUTC(t *testing.T) {
	local := time.Date(2026, 10, 8, 10, 0, 30, 0, time.FixedZone("UTC+5:30", 5*3600+1800))

	if got := (minuteSchedule{}).Next(local); got.Location() != time.UTC {
		t.Errorf("Next location = %s, want UTC", got.Location())
	}
}
