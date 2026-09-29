package jobqueue

import (
	"time"

	"github.com/riverqueue/river"
)

// ActivityDueInterval is how often the due-date reminder job runs
// (scheduled-activities.md §7).
const ActivityDueInterval = 15 * time.Minute

// ActivityDueArgs is the platform-wide periodic job that sends each open
// scheduled activity's engine.activity_due reminder once 08:00 on its due
// date has passed in its assignee's timezone (scheduled-activities.md §7)
// — registered as a river.PeriodicJob in New, not inserted by any caller.
// A single run fans out across every active tenant itself, the same shape
// as InviteExpiryArgs. Its worker lives in package engine, which holds
// the record reads a reminder is checked against.
type ActivityDueArgs struct{}

func (ActivityDueArgs) Kind() string { return "activity_due_reminders" }

func (ActivityDueArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{Queue: QueueDefault}
}
