package scheduledactivity

import (
	"context"
	"database/sql"
	"encoding/json/v2"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/db"
	"github.com/djangbahevans/goerp/internal/engine/recordactivity"
	"github.com/djangbahevans/goerp/internal/engine/tenantschema"
)

// localPostgresDSN points directly at the compose.dev.yml Postgres
// instance, same convention as internal/engine/recordactivity's tests.
const localPostgresDSN = "postgres://goerp:dev@localhost:55432/goerp"

const (
	testModel    = "sales.order"
	testRecordID = "11111111-1111-1111-1111-111111111111"
	testCreator  = "00000000-0000-0000-0000-0000000000aa"
	testAssignee = "00000000-0000-0000-0000-0000000000bb"
)

func openTestStore(t *testing.T) (store *Store, conn *sql.DB, slug string) {
	t.Helper()

	conn, err := db.New(localPostgresDSN)
	if err != nil {
		t.Skipf("postgres not reachable at %s (start compose.dev.yml): %v", localPostgresDSN, err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	slug = fmt.Sprintf("scheduledactivitytest%d", time.Now().UnixNano())
	schema := tenantschema.Name(slug)
	if _, err := conn.ExecContext(t.Context(), "CREATE SCHEMA "+schema); err != nil {
		t.Fatalf("create fixture schema: %v", err)
	}
	t.Cleanup(func() {
		_, _ = conn.ExecContext(context.Background(), "DROP SCHEMA "+schema+" CASCADE")
	})

	if err := recordactivity.NewStore(conn).Bootstrap(t.Context(), slug); err != nil {
		t.Fatalf("recordactivity Bootstrap() error: %v", err)
	}
	store = NewStore(conn)
	if err := store.Bootstrap(t.Context(), slug); err != nil {
		t.Fatalf("Bootstrap() error: %v", err)
	}
	return store, conn, slug
}

func create(t *testing.T, store *Store, slug, dueDate string) *Activity {
	t.Helper()
	a, err := store.Create(t.Context(), slug, NewActivity{
		Model: testModel, RecordID: testRecordID, Type: "call", Summary: "Confirm delivery",
		DueDate: dueDate, AssigneeID: testAssignee, CreatedBy: testCreator,
	})
	if err != nil {
		t.Fatalf("Create() error: %v", err)
	}
	return a
}

func TestBootstrap_IsIdempotent(t *testing.T) {
	store, _, slug := openTestStore(t)

	if err := store.Bootstrap(t.Context(), slug); err != nil {
		t.Fatalf("second Bootstrap() call error: %v", err)
	}
}

func TestBootstrap_CreatesBothPartialIndexes(t *testing.T) {
	_, conn, slug := openTestStore(t)

	for _, name := range []string{"idx_scheduled_activities_record", "idx_scheduled_activities_assignee"} {
		var def string
		err := conn.QueryRowContext(t.Context(), `SELECT indexdef FROM pg_indexes WHERE schemaname = $1 AND indexname = $2`, "tenant_"+slug, name).Scan(&def)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if want := "WHERE (done_at IS NULL)"; !strings.Contains(def, want) {
			t.Errorf("%s = %q, want a partial index %s", name, def, want)
		}
	}
}

func TestBootstrap_CheckConstraintsRejectInconsistentRows(t *testing.T) {
	_, conn, slug := openTestStore(t)
	schema := tenantschema.Name(slug)
	cols := `(model, record_id, type, summary, due_date, assignee_id, created_by`
	vals := `VALUES ('m', gen_random_uuid(), %s, %s, '2026-09-25', gen_random_uuid(), gen_random_uuid()`

	cases := map[string]string{
		"unknown type":            `INSERT INTO %s.scheduled_activities ` + cols + `) ` + fmt.Sprintf(vals, "'visit'", "'x'") + `)`,
		"empty summary":           `INSERT INTO %s.scheduled_activities ` + cols + `) ` + fmt.Sprintf(vals, "'call'", "''") + `)`,
		"over-long summary":       `INSERT INTO %s.scheduled_activities ` + cols + `) ` + fmt.Sprintf(vals, "'call'", "repeat('x', 201)") + `)`,
		"over-long note":          `INSERT INTO %s.scheduled_activities ` + cols + `, note) ` + fmt.Sprintf(vals, "'call'", "'x'") + `, repeat('x', 10001))`,
		"done_at without by":      `INSERT INTO %s.scheduled_activities ` + cols + `, done_at) ` + fmt.Sprintf(vals, "'call'", "'x'") + `, NOW())`,
		"feedback while open":     `INSERT INTO %s.scheduled_activities ` + cols + `, feedback) ` + fmt.Sprintf(vals, "'call'", "'x'") + `, 'done')`,
		"over-long feedback":      `INSERT INTO %s.scheduled_activities ` + cols + `, done_at, done_by, feedback) ` + fmt.Sprintf(vals, "'call'", "'x'") + `, NOW(), gen_random_uuid(), repeat('x', 10001))`,
		"done_by without done_at": `INSERT INTO %s.scheduled_activities ` + cols + `, done_by) ` + fmt.Sprintf(vals, "'call'", "'x'") + `, gen_random_uuid())`,
	}
	for name, stmt := range cases {
		if _, err := conn.ExecContext(t.Context(), fmt.Sprintf(stmt, schema)); err == nil {
			t.Errorf("%s: insert succeeded, want a CHECK violation", name)
		}
	}
}

func TestListOpenForRecord_ReturnsOnlyOpenActivitiesSoonestFirst(t *testing.T) {
	store, _, slug := openTestStore(t)
	later := create(t, store, slug, "2026-10-02")
	sooner := create(t, store, slug, "2026-09-25")
	done := create(t, store, slug, "2026-09-20")
	if _, err := store.MarkDone(t.Context(), slug, done.ID, testAssignee, nil, "", ""); err != nil {
		t.Fatalf("MarkDone() error: %v", err)
	}

	got, err := store.ListOpenForRecord(t.Context(), slug, testModel, testRecordID)
	if err != nil {
		t.Fatalf("ListOpenForRecord() error: %v", err)
	}
	if len(got) != 2 || got[0].ID != sooner.ID || got[1].ID != later.ID {
		t.Errorf("got %+v, want [%s, %s]", got, sooner.ID, later.ID)
	}
}

func TestListOpenForAssignee_PagesEveryOpenActivityExactlyOnceInDueOrder(t *testing.T) {
	store, _, slug := openTestStore(t)
	dates := []string{"2026-09-27", "2026-09-25", "2026-09-25", "2026-09-26", "2026-09-25"}
	for _, d := range dates {
		create(t, store, slug, d)
	}

	var got []Activity
	var cursor *Cursor
	for page := 0; ; page++ {
		if page > len(dates) {
			t.Fatal("pagination did not terminate")
		}
		items, hasMore, err := store.ListOpenForAssignee(t.Context(), slug, testAssignee, cursor, 2)
		if err != nil {
			t.Fatalf("ListOpenForAssignee() error: %v", err)
		}
		got = append(got, items...)
		if !hasMore {
			break
		}
		last := items[len(items)-1]
		cursor = &Cursor{DueDate: last.DueDate, ID: last.ID}
	}

	if len(got) != len(dates) {
		t.Fatalf("got %d activities, want %d", len(got), len(dates))
	}
	seen := map[string]bool{}
	for i, a := range got {
		if seen[a.ID] {
			t.Errorf("activity %s returned twice", a.ID)
		}
		seen[a.ID] = true
		if i > 0 {
			prev := got[i-1]
			if a.DueDate < prev.DueDate || (a.DueDate == prev.DueDate && a.ID < prev.ID) {
				t.Errorf("activity %d (%s, %s) sorts before %d (%s, %s)", i, a.DueDate, a.ID, i-1, prev.DueDate, prev.ID)
			}
		}
	}
}

func TestUpdate_ReassignmentClearsRemindedAt(t *testing.T) {
	store, conn, slug := openTestStore(t)
	a := create(t, store, slug, "2026-09-25")
	setReminded := fmt.Sprintf(`UPDATE %s.scheduled_activities SET reminded_at = NOW() WHERE id = $1`, tenantschema.Name(slug))
	if _, err := conn.ExecContext(t.Context(), setReminded, a.ID); err != nil {
		t.Fatalf("set reminded_at: %v", err)
	}

	same := testAssignee
	got, err := store.Update(t.Context(), slug, a.ID, Update{AssigneeID: &same, Summary: new("Reconfirm delivery")})
	if err != nil {
		t.Fatalf("Update() same assignee error: %v", err)
	}
	if got.RemindedAt == nil {
		t.Error("reminded_at cleared by an update that kept the assignee")
	}
	if got.Summary != "Reconfirm delivery" || got.Type != "call" {
		t.Errorf("got summary %q type %q, want the new summary and the unchanged type", got.Summary, got.Type)
	}

	other := testCreator
	got, err = store.Update(t.Context(), slug, a.ID, Update{AssigneeID: &other})
	if err != nil {
		t.Fatalf("Update() reassign error: %v", err)
	}
	if got.AssigneeID != other || got.RemindedAt != nil {
		t.Errorf("got assignee %s reminded_at %v, want %s and nil", got.AssigneeID, got.RemindedAt, other)
	}
}

func TestUpdate_SetsAndClearsTheNote(t *testing.T) {
	store, _, slug := openTestStore(t)
	a := create(t, store, slug, "2026-09-25")

	got, err := store.Update(t.Context(), slug, a.ID, Update{Note: new("Morning slot")})
	if err != nil || got.Note == nil || *got.Note != "Morning slot" {
		t.Fatalf("Update() note = %v, %v; want Morning slot", got, err)
	}
	got, err = store.Update(t.Context(), slug, a.ID, Update{DueDate: new("2026-10-01")})
	if err != nil || got.Note == nil || got.DueDate != "2026-10-01" {
		t.Fatalf("Update() due date = %+v, %v; want the note kept and the new date", got, err)
	}
	got, err = store.Update(t.Context(), slug, a.ID, Update{ClearNote: true})
	if err != nil || got.Note != nil {
		t.Fatalf("Update() clear note = %+v, %v; want a nil note", got, err)
	}
}

func TestMarkDone_WritesOneActivityDoneEntryWithTheSnapshot(t *testing.T) {
	store, conn, slug := openTestStore(t)
	a := create(t, store, slug, "2026-09-25")

	done, err := store.MarkDone(t.Context(), slug, a.ID, testAssignee, new("Customer confirmed Friday."), "req-1", "")
	if err != nil {
		t.Fatalf("MarkDone() error: %v", err)
	}
	if done.DoneAt == nil || done.DoneBy == nil || *done.DoneBy != testAssignee || done.Feedback == nil {
		t.Errorf("done = %+v, want done_at, done_by and feedback set", done)
	}

	if _, err := store.MarkDone(t.Context(), slug, a.ID, testAssignee, nil, "", ""); !errors.Is(err, ErrDone) {
		t.Errorf("second MarkDone() error = %v, want ErrDone", err)
	}

	rows, err := conn.QueryContext(t.Context(), fmt.Sprintf(
		`SELECT kind, activity, author_id, request_id FROM %s.record_activity WHERE model = $1 AND record_id = $2`, tenantschema.Name(slug),
	), testModel, testRecordID)
	if err != nil {
		t.Fatalf("query feed: %v", err)
	}
	defer rows.Close()
	var n int
	for rows.Next() {
		n++
		var kind, authorID string
		var requestID sql.NullString
		var raw []byte
		if err := rows.Scan(&kind, &raw, &authorID, &requestID); err != nil {
			t.Fatalf("scan feed entry: %v", err)
		}
		var snap map[string]any
		if err := json.Unmarshal(raw, &snap); err != nil {
			t.Fatalf("decode snapshot: %v", err)
		}
		want := map[string]any{"activity_id": a.ID, "type": "call", "summary": "Confirm delivery", "due_date": "2026-09-25", "feedback": "Customer confirmed Friday."}
		for k, v := range want {
			if snap[k] != v {
				t.Errorf("snapshot[%s] = %v, want %v", k, snap[k], v)
			}
		}
		if kind != "activity_done" || authorID != testAssignee || requestID.String != "req-1" {
			t.Errorf("entry = %s by %s (request %q), want activity_done by the completer with the request id", kind, authorID, requestID.String)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate feed: %v", err)
	}
	if n != 1 {
		t.Errorf("feed has %d entries, want exactly 1", n)
	}
}

func TestMarkDone_ConcurrentCompletionsWriteOneEntry(t *testing.T) {
	store, conn, slug := openTestStore(t)
	a := create(t, store, slug, "2026-09-25")

	const racers = 5
	var wg sync.WaitGroup
	errs := make(chan error, racers)
	for range racers {
		wg.Go(func() {
			_, err := store.MarkDone(t.Context(), slug, a.ID, testAssignee, nil, "", "")
			errs <- err
		})
	}
	wg.Wait()
	close(errs)
	var succeeded int
	for err := range errs {
		switch {
		case err == nil:
			succeeded++
		case !errors.Is(err, ErrDone):
			t.Errorf("MarkDone() error = %v, want nil or ErrDone", err)
		}
	}
	if succeeded != 1 {
		t.Errorf("%d completions succeeded, want 1", succeeded)
	}

	var n int
	if err := conn.QueryRowContext(t.Context(), fmt.Sprintf(`SELECT count(*) FROM %s.record_activity WHERE kind = 'activity_done'`, tenantschema.Name(slug))).Scan(&n); err != nil {
		t.Fatalf("count feed entries: %v", err)
	}
	if n != 1 {
		t.Errorf("feed has %d activity_done entries, want 1", n)
	}
}

func TestWrites_DistinguishMissingFromDone(t *testing.T) {
	store, _, slug := openTestStore(t)
	a := create(t, store, slug, "2026-09-25")
	if _, err := store.MarkDone(t.Context(), slug, a.ID, testAssignee, nil, "", ""); err != nil {
		t.Fatalf("MarkDone() error: %v", err)
	}
	missing := "99999999-9999-9999-9999-999999999999"

	if _, err := store.Update(t.Context(), slug, a.ID, Update{Summary: new("x")}); !errors.Is(err, ErrDone) {
		t.Errorf("Update(done) error = %v, want ErrDone", err)
	}
	if err := store.Cancel(t.Context(), slug, a.ID); !errors.Is(err, ErrDone) {
		t.Errorf("Cancel(done) error = %v, want ErrDone", err)
	}
	if _, err := store.Update(t.Context(), slug, missing, Update{Summary: new("x")}); !errors.Is(err, ErrNotFound) {
		t.Errorf("Update(missing) error = %v, want ErrNotFound", err)
	}
	if err := store.Cancel(t.Context(), slug, missing); !errors.Is(err, ErrNotFound) {
		t.Errorf("Cancel(missing) error = %v, want ErrNotFound", err)
	}
	if _, err := store.Get(t.Context(), slug, missing); !errors.Is(err, ErrNotFound) {
		t.Errorf("Get(missing) error = %v, want ErrNotFound", err)
	}
}

func TestCancel_DeletesTheRowAndWritesNoFeedEntry(t *testing.T) {
	store, conn, slug := openTestStore(t)
	a := create(t, store, slug, "2026-09-25")

	if err := store.Cancel(t.Context(), slug, a.ID); err != nil {
		t.Fatalf("Cancel() error: %v", err)
	}
	if _, err := store.Get(t.Context(), slug, a.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("Get() after cancel error = %v, want ErrNotFound", err)
	}
	var n int
	if err := conn.QueryRowContext(t.Context(), fmt.Sprintf(`SELECT count(*) FROM %s.record_activity`, tenantschema.Name(slug))).Scan(&n); err != nil {
		t.Fatalf("count feed entries: %v", err)
	}
	if n != 0 {
		t.Errorf("feed has %d entries after cancel, want 0", n)
	}
}
