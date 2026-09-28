// Package recordactivity is the per-tenant-schema record_activity table —
// each Postgres-backed record's feed of creation, tracked-field changes,
// comments, and completed scheduled activities (record-activity.md §3) —
// and its companion record_followers table, the users following each
// record (§8). Engine-owned rather than module models: only the engine
// writes them, and host.db rejects module SQL that names either
// (record-activity.md §5). Store also provides the reads and writes the
// built-in /_meta/activity endpoints (internal/engine's
// dispatchActivity*Route handlers) go through.
package recordactivity

import (
	"context"
	"database/sql"
	"encoding/json/jsontext"
	"errors"
	"fmt"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/db"
	"github.com/djangbahevans/goerp/internal/engine/tenantschema"
)

// TableName is the table's unqualified name, as module SQL would spell it.
const TableName = "record_activity"

// FollowersTable is record_followers' unqualified name: the users following
// each record (record-activity.md §8), created by Bootstrap alongside
// record_activity.
const FollowersTable = "record_followers"

const (
	KindCreated      = "created"
	KindChange       = "change"
	KindComment      = "comment"
	KindActivityDone = "activity_done"
)

var ErrNotFound = errors.New("record activity entry not found")

type Store struct {
	db *sql.DB
}

func NewStore(db *sql.DB) *Store {
	return &Store{db: db}
}

// Entry is one record_activity row.
type Entry struct {
	ID        string
	Model     string
	RecordID  string
	Kind      string
	Body      *string
	Changes   jsontext.Value
	Activity  jsontext.Value
	AuthorID  *string
	CreatedAt time.Time
	DeletedAt *time.Time
}

const entryColumns = `id, model, record_id, kind, body, changes, activity, author_id, created_at, deleted_at`

// List returns up to limit entries of (model, recordID)'s feed, newest
// first, starting after the entry whose id is cursor ("" for the first
// page). hasMore reports whether a further page exists.
func (s *Store) List(ctx context.Context, tenantSlug, model, recordID, cursor string, limit int) (entries []Entry, hasMore bool, err error) {
	var cursorArg any
	if cursor != "" {
		cursorArg = cursor
	}
	query := fmt.Sprintf(`
		SELECT %s
		FROM %s.record_activity
		WHERE model = $1 AND record_id = $2 AND ($3::uuid IS NULL OR id < $3::uuid)
		ORDER BY id DESC
		LIMIT $4
	`, entryColumns, tenantschema.Name(tenantSlug))

	rows, err := s.db.QueryContext(ctx, query, model, recordID, cursorArg, limit+1)
	if err != nil {
		return nil, false, fmt.Errorf("list record activity: %w", err)
	}
	defer rows.Close()

	entries = []Entry{}
	for rows.Next() {
		e, err := scanEntry(rows)
		if err != nil {
			return nil, false, fmt.Errorf("list record activity: %w", err)
		}
		entries = append(entries, *e)
	}
	if err := rows.Err(); err != nil {
		return nil, false, fmt.Errorf("list record activity: %w", err)
	}
	if len(entries) > limit {
		return entries[:limit], true, nil
	}
	return entries, false, nil
}

// CreateComment inserts a comment on (model, recordID) authored by
// authorID and returns the stored row. The author follows the record in
// the same transaction (record-activity.md §8).
func (s *Store) CreateComment(ctx context.Context, tenantSlug, model, recordID, authorID, body, requestID, traceID string) (*Entry, error) {
	query := fmt.Sprintf(`
		INSERT INTO %s.record_activity (model, record_id, kind, body, author_id, request_id, trace_id)
		VALUES ($1, $2, 'comment', $3, $4, NULLIF($5, ''), NULLIF($6, ''))
		RETURNING %s
	`, tenantschema.Name(tenantSlug), entryColumns)

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("create comment: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	e, err := scanEntry(tx.QueryRowContext(ctx, query, model, recordID, body, authorID, requestID, traceID))
	if err != nil {
		return nil, fmt.Errorf("create comment: %w", err)
	}
	if err := Follow(ctx, tx, tenantSlug, model, recordID, authorID); err != nil {
		return nil, fmt.Errorf("create comment: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("create comment: %w", err)
	}
	return e, nil
}

// Follower is one record_followers row.
type Follower struct {
	UserID    string
	CreatedAt time.Time
}

type execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// Follow makes userID a follower of (model, recordID) through ex, normally
// the transaction of the event that triggers the follow. A user already
// following keeps their original row. recordID is any so a scanned primary
// key value can be passed as-is.
func Follow(ctx context.Context, ex execer, tenantSlug, model string, recordID any, userID string) error {
	query := fmt.Sprintf(`
		INSERT INTO %s.record_followers (model, record_id, user_id)
		VALUES ($1, $2, $3)
		ON CONFLICT DO NOTHING
	`, tenantschema.Name(tenantSlug))

	if _, err := ex.ExecContext(ctx, query, model, recordID, userID); err != nil {
		return fmt.Errorf("follow record: %w", err)
	}
	return nil
}

// Follow makes userID a follower of (model, recordID); a no-op when they
// already follow it.
func (s *Store) Follow(ctx context.Context, tenantSlug, model, recordID, userID string) error {
	return Follow(ctx, s.db, tenantSlug, model, recordID, userID)
}

// Unfollow removes userID's follow of (model, recordID); a no-op when they
// don't follow it.
func (s *Store) Unfollow(ctx context.Context, tenantSlug, model, recordID, userID string) error {
	query := fmt.Sprintf(`
		DELETE FROM %s.record_followers
		WHERE model = $1 AND record_id = $2 AND user_id = $3
	`, tenantschema.Name(tenantSlug))

	if _, err := s.db.ExecContext(ctx, query, model, recordID, userID); err != nil {
		return fmt.Errorf("unfollow record: %w", err)
	}
	return nil
}

// ListFollowers returns every follower of (model, recordID), oldest first.
func (s *Store) ListFollowers(ctx context.Context, tenantSlug, model, recordID string) ([]Follower, error) {
	query := fmt.Sprintf(`
		SELECT user_id, created_at
		FROM %s.record_followers
		WHERE model = $1 AND record_id = $2
		ORDER BY created_at, user_id
	`, tenantschema.Name(tenantSlug))

	rows, err := s.db.QueryContext(ctx, query, model, recordID)
	if err != nil {
		return nil, fmt.Errorf("list followers: %w", err)
	}
	defer rows.Close()

	followers := []Follower{}
	for rows.Next() {
		var f Follower
		if err := rows.Scan(&f.UserID, &f.CreatedAt); err != nil {
			return nil, fmt.Errorf("list followers: %w", err)
		}
		followers = append(followers, f)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list followers: %w", err)
	}
	return followers, nil
}

// InsertActivityDone writes an activity_done entry authored by authorID
// inside tx, the transaction that marks the scheduled activity done.
// activity is the entry's {activity_id, type, summary, due_date, feedback}
// snapshot.
func InsertActivityDone(ctx context.Context, tx *sql.Tx, tenantSlug, model, recordID, authorID string, activity jsontext.Value, requestID, traceID string) error {
	query := fmt.Sprintf(`
		INSERT INTO %s.record_activity (model, record_id, kind, activity, author_id, request_id, trace_id)
		VALUES ($1, $2, 'activity_done', $3, $4, NULLIF($5, ''), NULLIF($6, ''))
	`, tenantschema.Name(tenantSlug))

	if _, err := tx.ExecContext(ctx, query, model, recordID, []byte(activity), authorID, requestID, traceID); err != nil {
		return fmt.Errorf("insert activity_done entry: %w", err)
	}
	return nil
}

// Get returns the entry with the given id, or ErrNotFound.
func (s *Store) Get(ctx context.Context, tenantSlug, id string) (*Entry, error) {
	query := fmt.Sprintf(`SELECT %s FROM %s.record_activity WHERE id = $1`, entryColumns, tenantschema.Name(tenantSlug))

	e, err := scanEntry(s.db.QueryRowContext(ctx, query, id))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("get record activity entry: %w", err)
	}
	return e, nil
}

// DeleteComment soft-deletes the comment with the given id: sets
// deleted_at and clears body. Deleting an already-deleted comment keeps
// its original deleted_at. Returns ErrNotFound if no comment has that id.
func (s *Store) DeleteComment(ctx context.Context, tenantSlug, id string) error {
	query := fmt.Sprintf(`
		UPDATE %s.record_activity
		SET deleted_at = COALESCE(deleted_at, NOW()), body = NULL
		WHERE id = $1 AND kind = 'comment'
	`, tenantschema.Name(tenantSlug))

	res, err := s.db.ExecContext(ctx, query, id)
	if err != nil {
		return fmt.Errorf("delete comment: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("delete comment: %w", err)
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanEntry(sc rowScanner) (*Entry, error) {
	var e Entry
	var changes, activity []byte
	if err := sc.Scan(&e.ID, &e.Model, &e.RecordID, &e.Kind, &e.Body, &changes, &activity, &e.AuthorID, &e.CreatedAt, &e.DeletedAt); err != nil {
		return nil, err
	}
	e.Changes = jsontext.Value(changes)
	e.Activity = jsontext.Value(activity)
	return &e, nil
}

// Bootstrap creates record_activity, its feed index, and record_followers
// in the given tenant's schema if they don't already exist. Does not create
// the schema itself. author_id and user_id are plain UUID columns with no
// FK — system.users lives outside tenant_{slug}. Concurrent-safe against
// other calls racing to bootstrap the same tenant's schema via
// db.WithAdvisoryLock.
func (s *Store) Bootstrap(ctx context.Context, tenantSlug string) error {
	keys := []int64{db.AdvisoryLockKey("recordactivity.Bootstrap:" + tenantSlug)}
	return db.WithAdvisoryLock(ctx, s.db, keys, func(tx *sql.Tx) error {
		schema := tenantschema.Name(tenantSlug)

		createTable := fmt.Sprintf(`
			CREATE TABLE IF NOT EXISTS %s.record_activity (
			    id          UUID PRIMARY KEY DEFAULT uuidv7(),
			    model       TEXT NOT NULL,
			    record_id   UUID NOT NULL,
			    kind        TEXT NOT NULL CHECK (kind IN ('created', 'change', 'comment', 'activity_done')),
			    body        TEXT,
			    changes     JSONB,
			    activity    JSONB,
			    author_id   UUID,
			    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			    deleted_at  TIMESTAMPTZ,
			    request_id  TEXT,
			    trace_id    TEXT,
			    CHECK ((kind = 'change') = (changes IS NOT NULL)),
			    CHECK ((kind = 'activity_done') = (activity IS NOT NULL)),
			    CHECK (kind = 'comment' OR deleted_at IS NULL)
			)
		`, schema)
		if _, err := tx.ExecContext(ctx, createTable); err != nil {
			return fmt.Errorf("create record_activity table: %w", err)
		}

		createIndex := fmt.Sprintf(`
			CREATE INDEX IF NOT EXISTS idx_record_activity_record
			    ON %s.record_activity (model, record_id, id DESC)
		`, schema)
		if _, err := tx.ExecContext(ctx, createIndex); err != nil {
			return fmt.Errorf("create record_activity index: %w", err)
		}

		// The primary key serves both reads, "a record's followers" and
		// "is this user following".
		createFollowers := fmt.Sprintf(`
			CREATE TABLE IF NOT EXISTS %s.record_followers (
			    model       TEXT NOT NULL,
			    record_id   UUID NOT NULL,
			    user_id     UUID NOT NULL,
			    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			    PRIMARY KEY (model, record_id, user_id)
			)
		`, schema)
		if _, err := tx.ExecContext(ctx, createFollowers); err != nil {
			return fmt.Errorf("create record_followers table: %w", err)
		}

		return nil
	})
}
