package engine

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/activitytype"
	"github.com/djangbahevans/goerp/internal/engine/notify"
	"github.com/djangbahevans/goerp/internal/engine/tenant"
	"github.com/djangbahevans/goerp/internal/engine/tenantschema"
	"github.com/djangbahevans/goerp/internal/engine/user"
)

// recordingNotifier is an engineNotifier that records what the engine
// sends instead of delivering it.
type recordingNotifier struct {
	mu        sync.Mutex
	sent      []sentNotification
	announced int
}

type sentNotification struct {
	notificationType, userID string
	data                     map[string]any
	opts                     notify.Options
	tx                       bool
}

func (n *recordingNotifier) Send(_ context.Context, _, moduleName, notificationType, userID string, data map[string]any, opts notify.Options) (*notify.Result, error) {
	return n.record(moduleName, notificationType, userID, data, opts, false)
}

func (n *recordingNotifier) SendTx(_ context.Context, _ *sql.Tx, _, moduleName, notificationType, userID string, data map[string]any, opts notify.Options) (*notify.Result, error) {
	return n.record(moduleName, notificationType, userID, data, opts, true)
}

func (n *recordingNotifier) record(moduleName, notificationType, userID string, data map[string]any, opts notify.Options, tx bool) (*notify.Result, error) {
	if moduleName != notify.EngineModule {
		return nil, fmt.Errorf("sent as module %q, want the engine", moduleName)
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	n.sent = append(n.sent, sentNotification{notificationType: notificationType, userID: userID, data: data, opts: opts, tx: tx})
	return &notify.Result{UserID: userID}, nil
}

func (n *recordingNotifier) Announce(context.Context, *notify.Result) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.announced++
}

// take returns what has been sent since the last take.
func (n *recordingNotifier) take() []sentNotification {
	n.mu.Lock()
	defer n.mu.Unlock()
	sent := n.sent
	n.sent = nil
	return sent
}

func newActivityNotifyFixture(t *testing.T) (*scheduledActivityFixture, *recordingNotifier) {
	t.Helper()
	f := newScheduledActivityFixture(t)
	n := &recordingNotifier{}
	f.e.notifier = n
	return f, n
}

func TestNotifyActivityAssigned_OnlyWhenAssignedToSomeoneElse(t *testing.T) {
	f, n := newActivityNotifyFixture(t)
	ctx := t.Context()
	if _, err := f.e.activityTypeStore.Update(ctx, f.slug, "call", activitytype.Update{Label: map[string]string{"en": "Call", "fr": "Appel"}}); err != nil {
		t.Fatalf("activity type Update() error: %v", err)
	}
	if err := f.e.userStore.EnsureProfile(ctx, f.memberID, "Kofi Boateng"); err != nil {
		t.Fatalf("EnsureProfile() error: %v", err)
	}
	if _, err := f.admin.ExecContext(ctx, `UPDATE system.user_profiles SET locale = 'fr' WHERE user_id = $1`, f.memberID); err != nil {
		t.Fatalf("set member locale: %v", err)
	}

	own := f.schedule(t, f.callerID, nil)
	if sent := n.take(); len(sent) != 0 {
		t.Errorf("scheduling for yourself sent %+v, want nothing", sent)
	}

	a := f.schedule(t, f.callerID, map[string]any{"assignee_id": f.memberID})
	sent := n.take()
	if len(sent) != 1 {
		t.Fatalf("assigning to the member sent %d notifications, want 1: %+v", len(sent), sent)
	}
	got := sent[0]
	if got.notificationType != "engine.activity_assigned" || got.userID != f.memberID || got.tx {
		t.Errorf("sent %s to %s (tx %v), want engine.activity_assigned to the member", got.notificationType, got.userID, got.tx)
	}
	want := map[string]any{
		"ActivityID": a["id"], "Model": activityTestModel, "RecordID": f.recordID, "RecordName": "Widget A",
		"Type": "call", "TypeLabel": "Appel", "TypeIcon": "phone", "Summary": "Confirm Friday delivery",
		"DueDate": "2026-09-25", "AssignedByName": "Ama Owusu",
	}
	for k, v := range want {
		if got.data[k] != v {
			t.Errorf("data[%s] = %v, want %v", k, got.data[k], v)
		}
	}

	// An edit that keeps the assignee sends nothing; reassigning to
	// someone else notifies them; the member taking their own activity
	// back from the caller's sends nothing.
	if w := f.patch(t, f.callerID, a["id"].(string), map[string]any{"summary": "Reconfirm"}); w.Code != http.StatusOK {
		t.Fatalf("PATCH status = %d; body: %s", w.Code, w.Body.String())
	}
	if sent := n.take(); len(sent) != 0 {
		t.Errorf("an edit keeping the assignee sent %+v, want nothing", sent)
	}
	if w := f.patch(t, f.callerID, own["id"].(string), map[string]any{"assignee_id": f.memberID}); w.Code != http.StatusOK {
		t.Fatalf("reassign PATCH status = %d; body: %s", w.Code, w.Body.String())
	}
	if sent := n.take(); len(sent) != 1 || sent[0].userID != f.memberID || sent[0].notificationType != "engine.activity_assigned" {
		t.Errorf("reassigning to the member sent %+v, want one engine.activity_assigned to them", sent)
	}
	if w := f.patch(t, f.memberID, own["id"].(string), map[string]any{"assignee_id": f.memberID}); w.Code != http.StatusOK {
		t.Fatalf("no-op reassign PATCH status = %d; body: %s", w.Code, w.Body.String())
	}
	if sent := n.take(); len(sent) != 0 {
		t.Errorf("reassigning to the current assignee sent %+v, want nothing", sent)
	}
}

// remind runs the reminder job's per-tenant pass at now.
func (f *scheduledActivityFixture) remind(t *testing.T, now string) {
	t.Helper()
	at, err := time.Parse(time.RFC3339, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.e.remindDueActivities(t.Context(), &tenant.Tenant{ID: f.tenantID, Slug: f.slug}, at); err != nil {
		t.Fatalf("remindDueActivities(%s) error: %v", now, err)
	}
}

func (f *scheduledActivityFixture) remindedAt(t *testing.T, id string) *time.Time {
	t.Helper()
	var at *time.Time
	if err := f.admin.QueryRowContext(t.Context(), fmt.Sprintf(`SELECT reminded_at FROM %s.scheduled_activities WHERE id = $1`, tenantschema.Name(f.slug)), id).Scan(&at); err != nil {
		t.Fatalf("read reminded_at: %v", err)
	}
	return at
}

func TestRemindDueActivities_OnceFromEightInTheAssigneesTimezone(t *testing.T) {
	f, n := newActivityNotifyFixture(t)
	ctx := t.Context()
	if err := f.e.userStore.EnsureProfile(ctx, f.memberID, "Kofi Boateng"); err != nil {
		t.Fatalf("EnsureProfile() error: %v", err)
	}
	if _, err := f.admin.ExecContext(ctx, `UPDATE system.user_profiles SET timezone = 'Asia/Tokyo' WHERE user_id = $1`, f.memberID); err != nil {
		t.Fatalf("set member timezone: %v", err)
	}

	// other has no timezone of their own, so theirs is the tenant's, UTC;
	// the member's 08:00 on the 25th is 23:00 UTC on the 24th.
	other := f.newUser(t, user.StatusActive, true)
	others := f.schedule(t, f.callerID, map[string]any{"assignee_id": other})
	members := f.schedule(t, f.callerID, map[string]any{"assignee_id": f.memberID})
	later := f.schedule(t, f.callerID, map[string]any{"assignee_id": other, "due_date": "2026-09-26"})
	n.take()

	f.remind(t, "2026-09-24T22:59:00Z")
	if sent := n.take(); len(sent) != 0 {
		t.Errorf("before 08:00 anywhere, sent %+v, want nothing", sent)
	}

	f.remind(t, "2026-09-24T23:00:00Z")
	sent := n.take()
	if len(sent) != 1 || sent[0].userID != f.memberID || sent[0].notificationType != "engine.activity_due" || !sent[0].tx {
		t.Fatalf("at 08:00 in Tokyo, sent %+v, want one engine.activity_due to the member, in the reminder's transaction", sent)
	}
	if sent[0].data["ActivityID"] != members["id"] || sent[0].data["Overdue"] != false {
		t.Errorf("reminder data = %v, want the member's activity, not overdue", sent[0].data)
	}
	if f.remindedAt(t, members["id"].(string)) == nil {
		t.Error("the member's activity has no reminded_at after its reminder")
	}

	f.remind(t, "2026-09-25T08:00:00Z")
	sent = n.take()
	if len(sent) != 1 || sent[0].userID != other || sent[0].data["ActivityID"] != others["id"] {
		t.Fatalf("at 08:00 UTC, sent %+v, want only the other member's reminder", sent)
	}
	if n.announced != 2 {
		t.Errorf("announced %d reminders, want 2", n.announced)
	}

	f.remind(t, "2026-09-25T23:00:00Z")
	if sent := n.take(); len(sent) != 0 {
		t.Errorf("a later run sent %+v, want nothing more", sent)
	}
	if f.remindedAt(t, later["id"].(string)) != nil {
		t.Error("the activity due on the 26th was reminded on the 25th")
	}

	// A day late, an unreminded activity is reminded as overdue.
	f.remind(t, "2026-09-27T09:00:00Z")
	sent = n.take()
	if len(sent) != 1 || sent[0].data["ActivityID"] != later["id"] || sent[0].data["Overdue"] != true {
		t.Errorf("a day late, sent %+v, want the overdue reminder for the 26th's activity", sent)
	}
}

func TestRemindDueActivities_ReassignmentRemindsTheNewAssignee(t *testing.T) {
	f, n := newActivityNotifyFixture(t)
	other := f.newUser(t, user.StatusActive, true)
	a := f.schedule(t, f.callerID, map[string]any{"assignee_id": f.memberID})
	id := a["id"].(string)
	done := f.schedule(t, f.callerID, map[string]any{"assignee_id": f.memberID, "summary": "Already handled"})
	if w := f.done(t, f.callerID, done["id"].(string), nil); w.Code != http.StatusOK {
		t.Fatalf("done status = %d; body: %s", w.Code, w.Body.String())
	}
	n.take()

	f.remind(t, "2026-09-25T09:00:00Z")
	if sent := n.take(); len(sent) != 1 || sent[0].userID != f.memberID || sent[0].data["ActivityID"] != id {
		t.Fatalf("first run sent %+v, want only the open activity's reminder to the member", sent)
	}

	if w := f.patch(t, f.callerID, id, map[string]any{"assignee_id": other}); w.Code != http.StatusOK {
		t.Fatalf("reassign PATCH status = %d; body: %s", w.Code, w.Body.String())
	}
	n.take()
	if f.remindedAt(t, id) != nil {
		t.Fatal("reassignment left reminded_at set")
	}

	f.remind(t, "2026-09-25T09:15:00Z")
	if sent := n.take(); len(sent) != 1 || sent[0].userID != other || sent[0].notificationType != "engine.activity_due" {
		t.Errorf("after reassignment, sent %+v, want the new assignee's own reminder", sent)
	}
}

func TestRemindDueActivities_SkipsAndMarksAnActivityWhoseRecordTheAssigneeCannotRead(t *testing.T) {
	f, n := newActivityNotifyFixture(t)
	f.restrictWidgetsToOwner(t)
	widget := f.insertWidget(t, "Soon private", nil)
	a := f.schedule(t, f.callerID, map[string]any{"record_id": widget, "assignee_id": f.memberID})
	n.take()

	if _, err := f.admin.ExecContext(t.Context(), fmt.Sprintf(`UPDATE %s.widget SET internal_ref = $1 WHERE id = $2`, tenantschema.Name(f.slug)), f.callerID, widget); err != nil {
		t.Fatalf("make widget private: %v", err)
	}

	f.remind(t, "2026-09-25T09:00:00Z")
	if sent := n.take(); len(sent) != 0 {
		t.Errorf("sent %+v for a record the assignee can no longer read, want nothing", sent)
	}
	if f.remindedAt(t, a["id"].(string)) == nil {
		t.Error("the skipped activity has no reminded_at, so every run would read it again")
	}
}

func TestRemindDueActivities_ConcurrentRunsRemindOnce(t *testing.T) {
	f, n := newActivityNotifyFixture(t)
	for range 5 {
		f.schedule(t, f.callerID, map[string]any{"assignee_id": f.memberID})
	}
	n.take()

	at, err := time.Parse(time.RFC3339, "2026-09-25T09:00:00Z")
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for range 4 {
		wg.Go(func() {
			if err := f.e.remindDueActivities(t.Context(), &tenant.Tenant{ID: f.tenantID, Slug: f.slug}, at); err != nil {
				t.Errorf("remindDueActivities() error: %v", err)
			}
		})
	}
	wg.Wait()

	seen := map[any]int{}
	for _, s := range n.take() {
		seen[s.data["ActivityID"]]++
	}
	if len(seen) != 5 {
		t.Errorf("reminded %d activities, want all 5", len(seen))
	}
	for id, count := range seen {
		if count != 1 {
			t.Errorf("activity %v reminded %d times, want once", id, count)
		}
	}
}

func TestReminderDue_FromEightLocalOnTheDueDate(t *testing.T) {
	accra := time.UTC
	tokyo, err := time.LoadLocation("Asia/Tokyo")
	if err != nil {
		t.Fatal(err)
	}
	newYork, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	at := func(s string) time.Time {
		tm, err := time.Parse(time.RFC3339, s)
		if err != nil {
			t.Fatal(err)
		}
		return tm
	}

	tests := []struct {
		name    string
		dueDate string
		now     time.Time
		zone    *time.Location
		want    bool
	}{
		{"before eight on the due date", "2026-09-25", at("2026-09-25T07:59:59Z"), accra, false},
		{"at eight on the due date", "2026-09-25", at("2026-09-25T08:00:00Z"), accra, true},
		{"a day overdue", "2026-09-24", at("2026-09-25T01:00:00Z"), accra, true},
		{"the day before", "2026-09-25", at("2026-09-24T23:00:00Z"), accra, false},
		// 08:00 in Tokyo is 23:00 UTC the day before.
		{"eight in Tokyo, still the day before in UTC", "2026-09-25", at("2026-09-24T23:00:00Z"), tokyo, true},
		{"seven in Tokyo", "2026-09-25", at("2026-09-24T22:59:00Z"), tokyo, false},
		// 2026-03-08 is New York's spring-forward day: 08:00 EDT is 12:00 UTC.
		{"before eight EDT on a DST day", "2026-03-08", at("2026-03-08T11:59:00Z"), newYork, false},
		{"eight EDT on a DST day", "2026-03-08", at("2026-03-08T12:00:00Z"), newYork, true},
		{"malformed due date", "25/09/2026", at("2026-09-26T12:00:00Z"), accra, false},
	}
	for _, tt := range tests {
		if got := reminderDue(tt.dueDate, tt.now, tt.zone); got != tt.want {
			t.Errorf("%s: reminderDue(%s, %s, %s) = %v, want %v", tt.name, tt.dueDate, tt.now.Format(time.RFC3339), tt.zone, got, tt.want)
		}
	}
}

func TestLoadZone_FallsBackForAnUnknownZone(t *testing.T) {
	fallback := time.FixedZone("fallback", 3600)
	for _, name := range []string{"", "Local", "Mars/Olympus_Mons"} {
		if got := loadZone(name, fallback); got != fallback {
			t.Errorf("loadZone(%q) = %v, want the fallback", name, got)
		}
	}
	if got := loadZone("Africa/Accra", fallback); got.String() != "Africa/Accra" {
		t.Errorf("loadZone(Africa/Accra) = %v", got)
	}
}

func TestRemindDueActivities_RetriesAfterARecordReadFails(t *testing.T) {
	f, n := newActivityNotifyFixture(t)
	f.restrictWidgetsToOwner(t)
	widget := f.insertWidget(t, "Shared", nil)
	a := f.schedule(t, f.callerID, map[string]any{"record_id": widget, "assignee_id": f.memberID})
	n.take()

	table := tenantschema.Name(f.slug) + ".widget"
	if _, err := f.admin.ExecContext(t.Context(), "REVOKE SELECT ON "+table+" FROM goerp_test_rls_reader_engine"); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	f.remind(t, "2026-09-25T09:00:00Z")
	if sent := n.take(); len(sent) != 0 {
		t.Errorf("sent %+v while the record read fails, want nothing", sent)
	}
	if f.remindedAt(t, a["id"].(string)) != nil {
		t.Fatal("a failed record read marked the activity reminded, so it would never be sent")
	}

	if _, err := f.admin.ExecContext(t.Context(), "GRANT SELECT ON "+table+" TO goerp_test_rls_reader_engine"); err != nil {
		t.Fatalf("grant: %v", err)
	}
	f.remind(t, "2026-09-25T09:15:00Z")
	if sent := n.take(); len(sent) != 1 || sent[0].userID != f.memberID {
		t.Errorf("once the read works, sent %+v, want the member's reminder", sent)
	}
}
