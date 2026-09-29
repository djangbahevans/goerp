package engine

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/activitytype"
	"github.com/djangbahevans/goerp/internal/engine/enginenotif"
	"github.com/djangbahevans/goerp/internal/engine/jobqueue"
	"github.com/djangbahevans/goerp/internal/engine/l10n"
	"github.com/djangbahevans/goerp/internal/engine/l10n/tenantl10n"
	"github.com/djangbahevans/goerp/internal/engine/notify"
	"github.com/djangbahevans/goerp/internal/engine/scheduledactivity"
	"github.com/djangbahevans/goerp/internal/engine/tenant"
	tenantresolve "github.com/djangbahevans/goerp/internal/engine/tenant/resolve"
	"github.com/djangbahevans/goerp/internal/engine/user"
	"github.com/riverqueue/river"
	"github.com/rs/zerolog/log"
)

// Scheduled activities' notifications (scheduled-activities.md §7):
// engine.activity_assigned when someone assigns an activity to another
// user, and engine.activity_due, the due-date reminder a periodic job
// sends.

// engineNotifier is the delivery pipeline as the engine sends its own
// engine.* types through it — satisfied by *notify.Sender.
type engineNotifier interface {
	Send(ctx context.Context, tenantID, moduleName, notificationType, userID string, data map[string]any, opts notify.Options) (*notify.Result, error)
	SendTx(ctx context.Context, tx *sql.Tx, tenantID, moduleName, notificationType, userID string, data map[string]any, opts notify.Options) (*notify.Result, error)
	Announce(ctx context.Context, res *notify.Result)
}

const (
	activityAssignedType = enginenotif.Module + "." + enginenotif.ActivityAssigned
	activityDueType      = enginenotif.Module + "." + enginenotif.ActivityDue

	// activityReminderHour is the assignee's local hour from which an
	// activity due that day is reminded.
	activityReminderHour = 8
	// activityReminderPage is how many candidates the reminder job reads
	// from a tenant at a time.
	activityReminderPage = 500
	// activityDueTimeout bounds one run of the reminder job.
	activityDueTimeout = 10 * time.Minute
)

// notifyActivityAssigned sends engine.activity_assigned to a's assignee,
// whom actorID has just assigned it to by creating or reassigning it —
// unless they assigned it to themselves. The activity is already saved,
// so a failed send is logged rather than failing the request.
func (e *Engine) notifyActivityAssigned(ctx context.Context, tenantCtx *tenantresolve.TenantContext, a *scheduledactivity.Activity, actorID string) {
	if e.notifier == nil || a.AssigneeID == actorID {
		return
	}
	err := func() error {
		recordName, readable, err := e.recordNameAs(ctx, tenantCtx, a.AssigneeID, a.Model, a.RecordID)
		if err != nil || !readable {
			return err
		}
		data, opts, err := e.activityNotification(ctx, tenantCtx, a, recordName)
		if err != nil {
			return err
		}
		if actor := e.newActivityAuthorResolver(tenantCtx.Slug).resolve(ctx, &actorID); actor != nil && actor.Name != nil {
			data["AssignedByName"] = *actor.Name
		}
		_, err = e.notifier.Send(ctx, tenantCtx.TenantID, notify.EngineModule, activityAssignedType, a.AssigneeID, data, opts)
		return err
	}()
	if err != nil {
		log.Error().Err(err).Str("tenant", tenantCtx.Slug).Str("activity_id", a.ID).Msg("scheduled activity: activity_assigned notification failed")
	}
}

// activityNotification is the data and options both of a's notifications
// carry (scheduled-activities.md §7): the activity's type label, resolved
// in the assignee's locale, and icon; its summary and due date; its
// record's display name as the assignee reads it; and a link to the
// record's form view.
func (e *Engine) activityNotification(ctx context.Context, tenantCtx *tenantresolve.TenantContext, a *scheduledactivity.Activity, recordName *string) (map[string]any, notify.Options, error) {
	settings, err := e.tenantL10n(ctx, tenantCtx.TenantID)
	if err != nil {
		return nil, notify.Options{}, err
	}
	locale := settings.DefaultLocale
	profile, err := e.userStore.GetProfile(ctx, a.AssigneeID)
	switch {
	case err == nil:
		if profile.Locale != nil {
			locale = *profile.Locale
		}
	case !errors.Is(err, user.ErrProfileNotFound):
		return nil, notify.Options{}, fmt.Errorf("load assignee profile: %w", err)
	}

	typeLabel, typeIcon := a.Type, ""
	t, err := e.activityTypeStore.Get(ctx, tenantCtx.Slug, a.Type)
	switch {
	case err == nil:
		if label, ok := activitytype.Resolve(t.Label, locale, settings.DefaultLocale); ok {
			typeLabel = label
		}
		typeIcon = t.Icon
	case !errors.Is(err, activitytype.ErrNotFound):
		return nil, notify.Options{}, fmt.Errorf("load activity type: %w", err)
	}

	name := ""
	if recordName != nil {
		name = *recordName
	}
	data := map[string]any{
		"ActivityID": a.ID,
		"Model":      a.Model,
		"RecordID":   a.RecordID,
		"RecordName": name,
		"Type":       a.Type,
		"TypeLabel":  typeLabel,
		"TypeIcon":   typeIcon,
		"Summary":    a.Summary,
		"DueDate":    a.DueDate,
	}
	var opts notify.Options
	if snap := e.moduleRegistry.Snapshot(); snap != nil {
		opts.ActionURL = snap.RecordFormPath(a.Model, a.RecordID)
	}
	return data, opts, nil
}

// recordNameAs reads recordID of modelName as userID, returning its
// display name and whether they can read it at all: not when they are no
// longer an active tenant member, the read doesn't return the record, or
// no loaded module declares the model any more. A read that fails for any
// other reason is an error, so a caller can try again rather than take
// the record for unreadable.
func (e *Engine) recordNameAs(ctx context.Context, tenantCtx *tenantresolve.TenantContext, userID, modelName, recordID string) (name *string, readable bool, err error) {
	permSet, member, err := e.memberPermissionSet(ctx, tenantCtx.Slug, userID)
	if err != nil || !member {
		return nil, false, err
	}
	names, err := e.recordNamesAs(ctx, tenantCtx, userID, permSet, modelName, []string{recordID})
	if errors.Is(err, errReadModelNotFound) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	name, readable = names[recordID]
	return name, readable, nil
}

// tenantL10n is the tenant's locale settings, or the platform defaults on
// an engine built without them.
func (e *Engine) tenantL10n(ctx context.Context, tenantID string) (tenantl10n.Settings, error) {
	if e.tenantLocales == nil {
		return tenantl10n.Settings{DefaultLocale: l10n.PlatformDefaultLocale, DefaultTimezone: l10n.PlatformDefaultTimezone}, nil
	}
	return e.tenantLocales.Load(ctx, tenantID)
}

// activityDueWorker works jobqueue.ActivityDueArgs. It is registered
// before the Engine it sends through exists, which New sets once built.
type activityDueWorker struct {
	river.WorkerDefaults[jobqueue.ActivityDueArgs]
	engine *Engine
}

// Timeout overrides River's one-minute default, which a pass over many
// tenants' reminders can exceed.
func (w *activityDueWorker) Timeout(*river.Job[jobqueue.ActivityDueArgs]) time.Duration {
	return activityDueTimeout
}

// Work sends each active tenant's due reminders independently, logging
// (not aborting on) a single tenant's failure, as InviteExpiryWorker does.
func (w *activityDueWorker) Work(ctx context.Context, job *river.Job[jobqueue.ActivityDueArgs]) error {
	e := w.engine
	if e == nil {
		return errors.New("activity due reminders: engine not ready")
	}
	tenants, err := e.tenantStore.ActiveTenants(ctx)
	if err != nil {
		return fmt.Errorf("list active tenants: %w", err)
	}
	now := time.Now()
	for i := range tenants {
		if err := e.remindDueActivities(ctx, &tenants[i], now); err != nil {
			log.Error().Err(err).Str("tenant", tenants[i].Slug).Msg("activity due reminders: tenant failed")
		}
	}
	return nil
}

// remindDueActivities sends t's due reminders as of now: one for each
// open, unreminded activity due today or earlier in its assignee's
// timezone (theirs, else the tenant's default, else the platform's) once
// it is 08:00 there. One activity failing is logged and the rest still
// go out.
func (e *Engine) remindDueActivities(ctx context.Context, t *tenant.Tenant, now time.Time) error {
	settings, err := e.tenantL10n(ctx, t.ID)
	if err != nil {
		return err
	}
	tenantZone := loadZone(settings.DefaultTimezone, time.UTC)
	tenantCtx := &tenantresolve.TenantContext{TenantID: t.ID, Slug: t.Slug, Name: t.Name, Plan: t.Plan, Region: t.Region, Status: t.Status}

	// No timezone is more than a day ahead of UTC, so nothing due after
	// tomorrow's UTC date is due anywhere yet.
	dueBy := now.UTC().AddDate(0, 0, 1).Format(time.DateOnly)
	var cursor *scheduledactivity.Cursor
	for {
		candidates, err := e.scheduledActivityStore.ListReminderCandidates(ctx, t.Slug, dueBy, cursor, activityReminderPage)
		if err != nil {
			return err
		}
		for _, c := range candidates {
			zone := tenantZone
			if c.AssigneeTimezone != nil {
				zone = loadZone(*c.AssigneeTimezone, tenantZone)
			}
			if !reminderDue(c.DueDate, now, zone) {
				continue
			}
			if err := e.sendDueReminder(ctx, tenantCtx, c.ID, c.AssigneeID, now, zone); err != nil {
				log.Error().Err(err).Str("tenant", t.Slug).Str("activity_id", c.ID).Msg("activity due reminders: reminder failed")
			}
		}
		if len(candidates) < activityReminderPage {
			return nil
		}
		last := candidates[len(candidates)-1]
		cursor = &scheduledactivity.Cursor{DueDate: last.DueDate, ID: last.ID}
	}
}

// sendDueReminder sends activity id's engine.activity_due to assigneeID
// and marks it reminded, in one transaction holding the activity's row
// lock (scheduledactivity.Store.Remind), so it is sent once however many
// runs overlap. It sends nothing when the activity has since been done,
// cancelled, reminded, or reassigned (a later run reminds the new
// assignee in their own timezone), and marks it reminded without sending
// when the assignee can no longer read its record or has left the tenant.
func (e *Engine) sendDueReminder(ctx context.Context, tenantCtx *tenantresolve.TenantContext, id, assigneeID string, now time.Time, zone *time.Location) error {
	var res *notify.Result
	err := e.scheduledActivityStore.Remind(ctx, tenantCtx.Slug, id, func(tx *sql.Tx, a *scheduledactivity.Activity) (bool, error) {
		if a.AssigneeID != assigneeID || !reminderDue(a.DueDate, now, zone) {
			return false, nil
		}
		recordName, readable, err := e.recordNameAs(ctx, tenantCtx, a.AssigneeID, a.Model, a.RecordID)
		if err != nil || !readable {
			return err == nil, err
		}
		data, opts, err := e.activityNotification(ctx, tenantCtx, a, recordName)
		if err != nil {
			return false, err
		}
		data["Overdue"] = a.DueDate < now.In(zone).Format(time.DateOnly)
		res, err = e.notifier.SendTx(ctx, tx, tenantCtx.TenantID, notify.EngineModule, activityDueType, a.AssigneeID, data, opts)
		if errors.Is(err, notify.ErrUnknownUser) {
			res = nil
			return true, nil
		}
		return err == nil, err
	})
	if errors.Is(err, scheduledactivity.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if res != nil {
		e.notifier.Announce(ctx, res)
	}
	return nil
}

// reminderDue reports whether an activity due on dueDate ("YYYY-MM-DD")
// is due its reminder at now in zone: it is 08:00 or later there on its
// due date or any day after.
func reminderDue(dueDate string, now time.Time, zone *time.Location) bool {
	due, err := time.Parse(time.DateOnly, dueDate)
	if err != nil {
		return false
	}
	return !now.Before(time.Date(due.Year(), due.Month(), due.Day(), activityReminderHour, 0, 0, 0, zone))
}

// loadZone loads the IANA zone name, or returns fallback for one this
// binary doesn't know.
func loadZone(name string, fallback *time.Location) *time.Location {
	if !l10n.ValidTimezone(name) {
		return fallback
	}
	zone, _ := time.LoadLocation(name)
	return zone
}
