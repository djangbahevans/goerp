package jobqueue

import (
	"time"

	"github.com/riverqueue/river"
)

// CronTickArgs is the platform-wide periodic job that fires, once a minute on
// the minute, every module cron job scheduled for that minute
// (manifest-spec.md §16) — registered as a river.PeriodicJob in New. River
// runs periodic jobs on its elected leader only, and At in the unique key
// keeps a leader handover from enqueueing a minute twice. Its worker lives in
// internal/engine/cronsched, which holds the module registry.
type CronTickArgs struct {
	// At is the UTC minute the tick stands for.
	At time.Time `json:"at" river:"unique"`
}

func (CronTickArgs) Kind() string { return "cron_tick" }

func (CronTickArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{
		Queue:      QueueAdmin,
		UniqueOpts: river.UniqueOpts{ByArgs: true, ByState: UniqueAcrossAllJobStates},
	}
}

// minuteSchedule runs on every UTC minute boundary, not a minute after
// startup like river.PeriodicInterval(time.Minute).
type minuteSchedule struct{}

func (minuteSchedule) Next(current time.Time) time.Time {
	return current.UTC().Truncate(time.Minute).Add(time.Minute)
}
