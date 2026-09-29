// Package scheduledactivity is the per-tenant-schema scheduled_activities
// table — planned calls, meetings, emails and to-dos attached to any
// Postgres-backed record (scheduled-activities.md §3). Engine-owned: only
// the engine writes it, and host.db rejects module SQL that names it. Store
// provides the reads and writes the built-in /_meta/scheduled-activities
// endpoint (internal/engine's dispatchScheduledActivity*Route handlers)
// goes through.
package scheduledactivity

import (
	"context"
	"database/sql"
	"encoding/json/v2"
	"errors"
	"fmt"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/db"
	"github.com/djangbahevans/goerp/internal/engine/recordactivity"
	"github.com/djangbahevans/goerp/internal/engine/tenantschema"
	"github.com/jackc/pgx/v5/pgconn"
)

// TableName is the table's unqualified name, as module SQL would spell it.
const TableName = "scheduled_activities"

var (
	ErrNotFound = errors.New("scheduled activity not found")
	// ErrDone is returned by a write to an activity that is already done.
	ErrDone = errors.New("scheduled activity is already done")
	// ErrUnknownType is returned by a write whose type isn't in the
	// tenant's activity_types, e.g. one deleted after the caller checked it.
	ErrUnknownType = errors.New("unknown activity type")
)

type Store struct {
	db *sql.DB
}

func NewStore(db *sql.DB) *Store {
	return &Store{db: db}
}

// Activity is one scheduled_activities row. DueDate is "YYYY-MM-DD".
type Activity struct {
	ID         string
	Model      string
	RecordID   string
	Type       string
	Summary    string
	Note       *string
	DueDate    string
	AssigneeID string
	CreatedBy  string
	CreatedAt  time.Time
	UpdatedAt  time.Time
	DoneAt     *time.Time
	DoneBy     *string
	Feedback   *string
	RemindedAt *time.Time
}

const activityColumns = `id, model, record_id, type, summary, note, to_char(due_date, 'YYYY-MM-DD'), assignee_id, created_by, created_at, updated_at, done_at, done_by, feedback, reminded_at`

// NewActivity is the input to Create.
type NewActivity struct {
	Model      string
	RecordID   string
	Type       string
	Summary    string
	Note       *string
	DueDate    string
	AssigneeID string
	CreatedBy  string
}

func (s *Store) Create(ctx context.Context, tenantSlug string, in NewActivity) (*Activity, error) {
	query := fmt.Sprintf(`
		INSERT INTO %s.scheduled_activities (model, record_id, type, summary, note, due_date, assignee_id, created_by)
		VALUES ($1, $2, $3, $4, $5, $6::date, $7, $8)
		RETURNING %s
	`, tenantschema.Name(tenantSlug), activityColumns)

	a, err := scanActivity(s.db.QueryRowContext(ctx, query, in.Model, in.RecordID, in.Type, in.Summary, in.Note, in.DueDate, in.AssigneeID, in.CreatedBy))
	if isForeignKeyViolation(err) {
		return nil, ErrUnknownType
	}
	if err != nil {
		return nil, fmt.Errorf("create scheduled activity: %w", err)
	}
	return a, nil
}

// Get returns the activity with the given id, or ErrNotFound.
func (s *Store) Get(ctx context.Context, tenantSlug, id string) (*Activity, error) {
	query := fmt.Sprintf(`SELECT %s FROM %s.scheduled_activities WHERE id = $1`, activityColumns, tenantschema.Name(tenantSlug))

	a, err := scanActivity(s.db.QueryRowContext(ctx, query, id))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("get scheduled activity: %w", err)
	}
	return a, nil
}

// ListOpenForRecord returns (model, recordID)'s open activities, soonest
// due first.
func (s *Store) ListOpenForRecord(ctx context.Context, tenantSlug, model, recordID string) ([]Activity, error) {
	query := fmt.Sprintf(`
		SELECT %s FROM %s.scheduled_activities
		WHERE model = $1 AND record_id = $2 AND done_at IS NULL
		ORDER BY due_date, id
	`, activityColumns, tenantschema.Name(tenantSlug))

	return s.list(ctx, query, model, recordID)
}

// Cursor is a position in ListOpenForAssignee's (due_date, id) order.
type Cursor struct {
	DueDate string
	ID      string
}

// ListOpenForAssignee returns up to limit of assigneeID's open activities,
// soonest due first with ties by id, starting after cursor (nil for the
// first page). hasMore reports whether a further page exists.
func (s *Store) ListOpenForAssignee(ctx context.Context, tenantSlug, assigneeID string, cursor *Cursor, limit int) (activities []Activity, hasMore bool, err error) {
	var cursorDate, cursorID any
	if cursor != nil {
		cursorDate, cursorID = cursor.DueDate, cursor.ID
	}
	query := fmt.Sprintf(`
		SELECT %s FROM %s.scheduled_activities
		WHERE assignee_id = $1 AND done_at IS NULL
		  AND ($2::date IS NULL OR (due_date, id) > ($2::date, $3::uuid))
		ORDER BY due_date, id
		LIMIT $4
	`, activityColumns, tenantschema.Name(tenantSlug))

	activities, err = s.list(ctx, query, assigneeID, cursorDate, cursorID, limit+1)
	if err != nil {
		return nil, false, err
	}
	if len(activities) > limit {
		return activities[:limit], true, nil
	}
	return activities, false, nil
}

func (s *Store) list(ctx context.Context, query string, args ...any) ([]Activity, error) {
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list scheduled activities: %w", err)
	}
	defer rows.Close()

	activities := []Activity{}
	for rows.Next() {
		a, err := scanActivity(rows)
		if err != nil {
			return nil, fmt.Errorf("list scheduled activities: %w", err)
		}
		activities = append(activities, *a)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list scheduled activities: %w", err)
	}
	return activities, nil
}

// Update is the input to Store.Update; a nil field is left unchanged.
// ClearNote sets note to NULL and takes precedence over Note.
type Update struct {
	Type       *string
	Summary    *string
	Note       *string
	ClearNote  bool
	DueDate    *string
	AssigneeID *string
}

// Update applies u to the open activity id. Changing the assignee clears
// reminded_at, so the new assignee gets their own due-date reminder.
// Returns ErrNotFound or ErrDone when there is no open activity to update.
func (s *Store) Update(ctx context.Context, tenantSlug, id string, u Update) (*Activity, error) {
	query := fmt.Sprintf(`
		UPDATE %s.scheduled_activities SET
		    type        = COALESCE($2, type),
		    summary     = COALESCE($3, summary),
		    note        = CASE WHEN $4::boolean THEN NULL ELSE COALESCE($5, note) END,
		    due_date    = COALESCE($6::date, due_date),
		    assignee_id = COALESCE($7::uuid, assignee_id),
		    reminded_at = CASE WHEN $7::uuid <> assignee_id THEN NULL ELSE reminded_at END,
		    updated_at  = NOW()
		WHERE id = $1 AND done_at IS NULL
		RETURNING %s
	`, tenantschema.Name(tenantSlug), activityColumns)

	a, err := scanActivity(s.db.QueryRowContext(ctx, query, id, u.Type, u.Summary, u.ClearNote, u.Note, u.DueDate, u.AssigneeID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, s.notOpenError(ctx, tenantSlug, id)
	}
	if isForeignKeyViolation(err) {
		return nil, ErrUnknownType
	}
	if err != nil {
		return nil, fmt.Errorf("update scheduled activity: %w", err)
	}
	return a, nil
}

// ReminderCandidate is an open, unreminded activity the due-date reminder
// job (scheduled-activities.md §7) considers, with its assignee's own
// timezone — nil when they haven't chosen one.
type ReminderCandidate struct {
	Activity
	AssigneeTimezone *string
}

// ListReminderCandidates returns up to limit open activities with no
// reminder sent yet that fall due on or before dueBy ("YYYY-MM-DD"),
// ordered by (due_date, id) and starting after cursor (nil for the
// first page). Whether one is due yet depends on its assignee's
// timezone, which the caller decides.
func (s *Store) ListReminderCandidates(ctx context.Context, tenantSlug, dueBy string, cursor *Cursor, limit int) ([]ReminderCandidate, error) {
	var cursorDate, cursorID any
	if cursor != nil {
		cursorDate, cursorID = cursor.DueDate, cursor.ID
	}
	query := fmt.Sprintf(`
		SELECT %s, (SELECT p.timezone FROM system.user_profiles p WHERE p.user_id = assignee_id)
		FROM %s.scheduled_activities
		WHERE done_at IS NULL AND reminded_at IS NULL AND due_date <= $1::date
		  AND ($2::date IS NULL OR (due_date, id) > ($2::date, $3::uuid))
		ORDER BY due_date, id
		LIMIT $4
	`, activityColumns, tenantschema.Name(tenantSlug))

	rows, err := s.db.QueryContext(ctx, query, dueBy, cursorDate, cursorID, limit)
	if err != nil {
		return nil, fmt.Errorf("list reminder candidates: %w", err)
	}
	defer rows.Close()

	var out []ReminderCandidate
	for rows.Next() {
		var c ReminderCandidate
		if err := rows.Scan(activityFields(&c.Activity, &c.AssigneeTimezone)...); err != nil {
			return nil, fmt.Errorf("list reminder candidates: %w", err)
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list reminder candidates: %w", err)
	}
	return out, nil
}

// Remind sends the activity id's due-date reminder: in one transaction it
// locks the activity, calls send with the transaction and the activity as
// it now is, and — when send reports it reminded — sets reminded_at, so
// the reminder commits together with whatever send wrote. It returns
// ErrNotFound, without calling send, when the activity is no longer open
// and unreminded, or when another transaction holds its lock and is
// already reminding it; so overlapping runs never both remind. send
// returning false or an error leaves the activity as it was.
func (s *Store) Remind(ctx context.Context, tenantSlug, id string, send func(tx *sql.Tx, a *Activity) (reminded bool, err error)) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("remind scheduled activity: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	schema := tenantschema.Name(tenantSlug)
	a, err := scanActivity(tx.QueryRowContext(ctx, fmt.Sprintf(`
		SELECT %s FROM %s.scheduled_activities
		WHERE id = $1 AND done_at IS NULL AND reminded_at IS NULL
		FOR UPDATE SKIP LOCKED
	`, activityColumns, schema), id))
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("remind scheduled activity: %w", err)
	}

	reminded, err := send(tx, a)
	if err != nil || !reminded {
		return err
	}
	if _, err := tx.ExecContext(ctx, fmt.Sprintf(`UPDATE %s.scheduled_activities SET reminded_at = NOW() WHERE id = $1`, schema), id); err != nil {
		return fmt.Errorf("remind scheduled activity: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("remind scheduled activity: %w", err)
	}
	return nil
}

// feedSnapshot is an activity_done feed entry's activity object
// (record-activity.md §1, §6).
type feedSnapshot struct {
	ActivityID string  `json:"activity_id"`
	Type       string  `json:"type"`
	Summary    string  `json:"summary"`
	DueDate    string  `json:"due_date"`
	Feedback   *string `json:"feedback"`
}

// MarkDone marks the open activity id done by doneBy with feedback, and in
// the same transaction writes the activity_done entry to its record's
// feed. Returns ErrNotFound or ErrDone when there is no open activity to
// complete, so a repeated or concurrent completion writes no second entry.
func (s *Store) MarkDone(ctx context.Context, tenantSlug, id, doneBy string, feedback *string, requestID, traceID string) (*Activity, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("mark scheduled activity done: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	query := fmt.Sprintf(`
		UPDATE %s.scheduled_activities
		SET done_at = NOW(), done_by = $2, feedback = $3, updated_at = NOW()
		WHERE id = $1 AND done_at IS NULL
		RETURNING %s
	`, tenantschema.Name(tenantSlug), activityColumns)

	a, err := scanActivity(tx.QueryRowContext(ctx, query, id, doneBy, feedback))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, s.notOpenError(ctx, tenantSlug, id)
	}
	if err != nil {
		return nil, fmt.Errorf("mark scheduled activity done: %w", err)
	}

	snapshot, err := json.Marshal(feedSnapshot{ActivityID: a.ID, Type: a.Type, Summary: a.Summary, DueDate: a.DueDate, Feedback: a.Feedback})
	if err != nil {
		return nil, fmt.Errorf("mark scheduled activity done: %w", err)
	}
	if err := recordactivity.InsertActivityDone(ctx, tx, tenantSlug, a.Model, a.RecordID, doneBy, snapshot, requestID, traceID); err != nil {
		return nil, fmt.Errorf("mark scheduled activity done: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("mark scheduled activity done: %w", err)
	}
	return a, nil
}

// Cancel deletes the open activity id. Returns ErrNotFound or ErrDone when
// there is no open activity to cancel.
func (s *Store) Cancel(ctx context.Context, tenantSlug, id string) error {
	query := fmt.Sprintf(`DELETE FROM %s.scheduled_activities WHERE id = $1 AND done_at IS NULL`, tenantschema.Name(tenantSlug))

	res, err := s.db.ExecContext(ctx, query, id)
	if err != nil {
		return fmt.Errorf("cancel scheduled activity: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("cancel scheduled activity: %w", err)
	}
	if n == 0 {
		return s.notOpenError(ctx, tenantSlug, id)
	}
	return nil
}

// notOpenError tells a missing activity from a done one after a write
// guarded by done_at IS NULL matched no row.
func (s *Store) notOpenError(ctx context.Context, tenantSlug, id string) error {
	if _, err := s.Get(ctx, tenantSlug, id); err != nil {
		return err
	}
	return ErrDone
}

func isForeignKeyViolation(err error) bool {
	pgErr, ok := errors.AsType[*pgconn.PgError](err)
	return ok && pgErr.Code == "23503"
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanActivity(sc rowScanner) (*Activity, error) {
	var a Activity
	if err := sc.Scan(activityFields(&a)...); err != nil {
		return nil, err
	}
	return &a, nil
}

// activityFields are the scan destinations for activityColumns into a,
// followed by extra.
func activityFields(a *Activity, extra ...any) []any {
	return append([]any{&a.ID, &a.Model, &a.RecordID, &a.Type, &a.Summary, &a.Note, &a.DueDate, &a.AssigneeID, &a.CreatedBy,
		&a.CreatedAt, &a.UpdatedAt, &a.DoneAt, &a.DoneBy, &a.Feedback, &a.RemindedAt}, extra...)
}

// Bootstrap creates scheduled_activities and its partial indexes in the
// given tenant's schema if they don't already exist. Does not create the
// schema itself, or activity_types, which type references and must exist
// first (activitytype.Store.Bootstrap). The users.id columns are plain
// UUIDs with no FK — system.users lives outside tenant_{slug}.
// Concurrent-safe against other calls racing to bootstrap the same
// tenant's schema via db.WithAdvisoryLock.
func (s *Store) Bootstrap(ctx context.Context, tenantSlug string) error {
	keys := []int64{db.AdvisoryLockKey("scheduledactivity.Bootstrap:" + tenantSlug)}
	return db.WithAdvisoryLock(ctx, s.db, keys, func(tx *sql.Tx) error {
		schema := tenantschema.Name(tenantSlug)

		createTable := fmt.Sprintf(`
			CREATE TABLE IF NOT EXISTS %[1]s.scheduled_activities (
			    id           UUID PRIMARY KEY DEFAULT uuidv7(),
			    model        TEXT NOT NULL,
			    record_id    UUID NOT NULL,
			    type         TEXT NOT NULL REFERENCES %[1]s.activity_types (key) ON DELETE RESTRICT,
			    summary      TEXT NOT NULL CHECK (char_length(summary) BETWEEN 1 AND 200),
			    note         TEXT CHECK (char_length(note) <= 10000),
			    due_date     DATE NOT NULL,
			    assignee_id  UUID NOT NULL,
			    created_by   UUID NOT NULL,
			    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			    updated_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			    done_at      TIMESTAMPTZ,
			    done_by      UUID,
			    feedback     TEXT CHECK (char_length(feedback) <= 10000),
			    reminded_at  TIMESTAMPTZ,
			    CHECK ((done_at IS NULL) = (done_by IS NULL)),
			    CHECK (done_at IS NOT NULL OR feedback IS NULL)
			)
		`, schema)
		if _, err := tx.ExecContext(ctx, createTable); err != nil {
			return fmt.Errorf("create scheduled_activities table: %w", err)
		}

		indexes := []string{
			`CREATE INDEX IF NOT EXISTS idx_scheduled_activities_record
			    ON %s.scheduled_activities (model, record_id, due_date) WHERE done_at IS NULL`,
			`CREATE INDEX IF NOT EXISTS idx_scheduled_activities_assignee
			    ON %s.scheduled_activities (assignee_id, due_date, id) WHERE done_at IS NULL`,
			`CREATE INDEX IF NOT EXISTS idx_scheduled_activities_reminder
			    ON %s.scheduled_activities (due_date, id) WHERE done_at IS NULL AND reminded_at IS NULL`,
		}
		for _, idx := range indexes {
			if _, err := tx.ExecContext(ctx, fmt.Sprintf(idx, schema)); err != nil {
				return fmt.Errorf("create scheduled_activities index: %w", err)
			}
		}
		return nil
	})
}
