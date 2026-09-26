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
	"slices"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/db"
	"github.com/djangbahevans/goerp/internal/engine/recordactivity"
	"github.com/djangbahevans/goerp/internal/engine/tenantschema"
)

// TableName is the table's unqualified name, as module SQL would spell it.
const TableName = "scheduled_activities"

// Types are the activity types a scheduled activity can have.
var Types = []string{"call", "meeting", "email", "todo"}

func ValidType(t string) bool { return slices.Contains(Types, t) }

var (
	ErrNotFound = errors.New("scheduled activity not found")
	// ErrDone is returned by a write to an activity that is already done.
	ErrDone = errors.New("scheduled activity is already done")
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
	if err != nil {
		return nil, fmt.Errorf("update scheduled activity: %w", err)
	}
	return a, nil
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

type rowScanner interface {
	Scan(dest ...any) error
}

func scanActivity(sc rowScanner) (*Activity, error) {
	var a Activity
	if err := sc.Scan(&a.ID, &a.Model, &a.RecordID, &a.Type, &a.Summary, &a.Note, &a.DueDate, &a.AssigneeID, &a.CreatedBy,
		&a.CreatedAt, &a.UpdatedAt, &a.DoneAt, &a.DoneBy, &a.Feedback, &a.RemindedAt); err != nil {
		return nil, err
	}
	return &a, nil
}

// Bootstrap creates scheduled_activities and its partial indexes in the
// given tenant's schema if they don't already exist. Does not create the
// schema itself. The users.id columns are plain UUIDs with no FK —
// system.users lives outside tenant_{slug}. Concurrent-safe against other
// calls racing to bootstrap the same tenant's schema via
// db.WithAdvisoryLock.
func (s *Store) Bootstrap(ctx context.Context, tenantSlug string) error {
	keys := []int64{db.AdvisoryLockKey("scheduledactivity.Bootstrap:" + tenantSlug)}
	return db.WithAdvisoryLock(ctx, s.db, keys, func(tx *sql.Tx) error {
		schema := tenantschema.Name(tenantSlug)

		createTable := fmt.Sprintf(`
			CREATE TABLE IF NOT EXISTS %s.scheduled_activities (
			    id           UUID PRIMARY KEY DEFAULT uuidv7(),
			    model        TEXT NOT NULL,
			    record_id    UUID NOT NULL,
			    type         TEXT NOT NULL CHECK (type IN ('call', 'meeting', 'email', 'todo')),
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
		}
		for _, idx := range indexes {
			if _, err := tx.ExecContext(ctx, fmt.Sprintf(idx, schema)); err != nil {
				return fmt.Errorf("create scheduled_activities index: %w", err)
			}
		}
		return nil
	})
}
