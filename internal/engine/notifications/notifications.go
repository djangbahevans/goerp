// Package notifications is the per-tenant-schema notification tables
// (notification-system.md §3): the in-app feed (notifications), push
// device tokens (user_device_tokens) and per-user channel preferences
// (notification_preferences). Engine-owned rather than module models: only
// the engine writes them, and host.db rejects module SQL that names one.
// Store also provides the reads and writes the built-in /_notif/* routes
// (internal/engine's dispatchNotif*Route handlers) go through.
package notifications

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
	"uuid"

	"github.com/djangbahevans/goerp/internal/engine/cache"
	"github.com/djangbahevans/goerp/internal/engine/db"
	"github.com/djangbahevans/goerp/internal/engine/tenantschema"
)

// FeedTable is the feed table's unqualified name, as module SQL would
// spell it.
const FeedTable = "notifications"

var (
	ErrNotFound      = errors.New("notification not found")
	ErrInvalidCursor = errors.New("malformed feed cursor")
)

type Store struct {
	db    *sql.DB
	cache *cache.Client
	// afterLoad, when set, runs between Preferences' database read and its
	// cache write; tests use it to interleave a write.
	afterLoad func()
}

func NewStore(db *sql.DB) *Store {
	return &Store{db: db}
}

// Notification is one notifications row.
type Notification struct {
	ID          string
	UserID      string
	Type        string
	Module      string
	Title       string
	Body        *string
	ActionURL   *string
	Icon        *string
	ReadAt      *time.Time
	DismissedAt *time.Time
	CreatedAt   time.Time
}

const notificationColumns = `id, user_id, type, module, title, body, action_url, icon, read_at, dismissed_at, created_at`

// Cursor is a feed position: the last notification a page returned, and
// whether it was unread at the time. The feed lists unread before read, so
// the next page resumes inside the section the cursor was in.
type Cursor struct {
	ID     string
	Unread bool
}

// String encodes c as "u.<id>" or "r.<id>", the opaque cursor the feed
// route hands out.
func (c Cursor) String() string {
	if c.Unread {
		return "u." + c.ID
	}
	return "r." + c.ID
}

// ParseCursor decodes a Cursor.String value, returning ErrInvalidCursor
// for anything else.
func ParseCursor(s string) (Cursor, error) {
	section, id, ok := strings.Cut(s, ".")
	if !ok || (section != "u" && section != "r") {
		return Cursor{}, ErrInvalidCursor
	}
	if _, err := uuid.Parse(id); err != nil {
		return Cursor{}, ErrInvalidCursor
	}
	return Cursor{ID: id, Unread: section == "u"}, nil
}

// CursorAfter is the cursor that resumes the feed after n.
func CursorAfter(n *Notification) Cursor {
	return Cursor{ID: n.ID, Unread: n.ReadAt == nil}
}

// List returns up to limit of userID's non-dismissed notifications, unread
// first and newest first within each, starting after cursor (nil for the
// first page). unreadOnly drops read ones. hasMore reports whether a
// further page exists.
func (s *Store) List(ctx context.Context, tenantSlug, tenantID, userID string, cursor *Cursor, unreadOnly bool, limit int) (items []Notification, hasMore bool, err error) {
	var cursorID any
	cursorUnread := false
	if cursor != nil {
		cursorID, cursorUnread = cursor.ID, cursor.Unread
	}
	// Past an unread cursor come the older unread rows, then every read
	// one; past a read cursor, only the older read rows.
	query := fmt.Sprintf(`
		SELECT %s
		FROM %s.notifications
		WHERE tenant_id = $1 AND user_id = $2 AND dismissed_at IS NULL
		  AND (NOT $3 OR read_at IS NULL)
		  AND ($4::uuid IS NULL
		       OR ($5 AND ((read_at IS NULL AND id < $4::uuid) OR read_at IS NOT NULL))
		       OR (NOT $5 AND read_at IS NOT NULL AND id < $4::uuid))
		ORDER BY (read_at IS NULL) DESC, id DESC
		LIMIT $6
	`, notificationColumns, tenantschema.Name(tenantSlug))

	rows, err := s.db.QueryContext(ctx, query, tenantID, userID, unreadOnly, cursorID, cursorUnread, limit+1)
	if err != nil {
		return nil, false, fmt.Errorf("list notifications: %w", err)
	}
	defer rows.Close()

	items = []Notification{}
	for rows.Next() {
		n, err := scanNotification(rows)
		if err != nil {
			return nil, false, fmt.Errorf("list notifications: %w", err)
		}
		items = append(items, *n)
	}
	if err := rows.Err(); err != nil {
		return nil, false, fmt.Errorf("list notifications: %w", err)
	}
	if len(items) > limit {
		return items[:limit], true, nil
	}
	return items, false, nil
}

// CountUnread returns how many of userID's notifications are unread and
// not dismissed.
func (s *Store) CountUnread(ctx context.Context, tenantSlug, tenantID, userID string) (int, error) {
	query := fmt.Sprintf(`
		SELECT count(*) FROM %s.notifications
		WHERE tenant_id = $1 AND user_id = $2 AND read_at IS NULL AND dismissed_at IS NULL
	`, tenantschema.Name(tenantSlug))

	var n int
	if err := s.db.QueryRowContext(ctx, query, tenantID, userID).Scan(&n); err != nil {
		return 0, fmt.Errorf("count unread notifications: %w", err)
	}
	return n, nil
}

// MarkRead sets read_at on userID's notification id, keeping an existing
// read_at. Returns ErrNotFound when userID has no notification with that id.
func (s *Store) MarkRead(ctx context.Context, tenantSlug, tenantID, userID, id string) error {
	return s.updateOne(ctx, tenantSlug, tenantID, userID, id, "read_at = COALESCE(read_at, NOW())", "mark notification read")
}

// Dismiss sets dismissed_at on userID's notification id, keeping an
// existing dismissed_at. Returns ErrNotFound when userID has no
// notification with that id.
func (s *Store) Dismiss(ctx context.Context, tenantSlug, tenantID, userID, id string) error {
	return s.updateOne(ctx, tenantSlug, tenantID, userID, id, "dismissed_at = COALESCE(dismissed_at, NOW())", "dismiss notification")
}

// MarkAllRead sets read_at on every unread notification of userID's.
func (s *Store) MarkAllRead(ctx context.Context, tenantSlug, tenantID, userID string) error {
	return s.updateAll(ctx, tenantSlug, tenantID, userID, "read_at = NOW()", "read_at IS NULL", "mark all notifications read")
}

// DismissAll sets dismissed_at on every non-dismissed notification of
// userID's.
func (s *Store) DismissAll(ctx context.Context, tenantSlug, tenantID, userID string) error {
	return s.updateAll(ctx, tenantSlug, tenantID, userID, "dismissed_at = NOW()", "dismissed_at IS NULL", "dismiss all notifications")
}

func (s *Store) updateOne(ctx context.Context, tenantSlug, tenantID, userID, id, set, op string) error {
	query := fmt.Sprintf(`UPDATE %s.notifications SET %s WHERE id = $1 AND tenant_id = $2 AND user_id = $3`, tenantschema.Name(tenantSlug), set)

	res, err := s.db.ExecContext(ctx, query, id, tenantID, userID)
	if err != nil {
		return fmt.Errorf("%s: %w", op, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("%s: %w", op, err)
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) updateAll(ctx context.Context, tenantSlug, tenantID, userID, set, where, op string) error {
	query := fmt.Sprintf(`UPDATE %s.notifications SET %s WHERE tenant_id = $1 AND user_id = $2 AND %s`, tenantschema.Name(tenantSlug), set, where)

	if _, err := s.db.ExecContext(ctx, query, tenantID, userID); err != nil {
		return fmt.Errorf("%s: %w", op, err)
	}
	return nil
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanNotification(sc rowScanner) (*Notification, error) {
	var n Notification
	if err := sc.Scan(&n.ID, &n.UserID, &n.Type, &n.Module, &n.Title, &n.Body, &n.ActionURL, &n.Icon, &n.ReadAt, &n.DismissedAt, &n.CreatedAt); err != nil {
		return nil, err
	}
	return &n, nil
}

// BootstrapFeed creates notifications and its two partial feed indexes in
// the given tenant's schema if they don't already exist. Does not create
// the schema itself. user_id is a plain UUID column with no FK —
// system.users lives outside tenant_{slug}. Concurrent-safe against other
// calls racing to bootstrap the same tenant's schema via
// db.WithAdvisoryLock.
func (s *Store) BootstrapFeed(ctx context.Context, tenantSlug string) error {
	schema := tenantschema.Name(tenantSlug)
	return s.bootstrap(ctx, "notifications.BootstrapFeed:"+tenantSlug, []string{
		fmt.Sprintf(`
			CREATE TABLE IF NOT EXISTS %s.notifications (
			    id            UUID PRIMARY KEY DEFAULT uuidv7(),
			    tenant_id     UUID NOT NULL,
			    user_id       UUID NOT NULL,
			    type          TEXT NOT NULL,
			    module        TEXT NOT NULL,
			    title         TEXT NOT NULL,
			    body          TEXT,
			    action_url    TEXT,
			    icon          TEXT,
			    data          JSONB NOT NULL DEFAULT '{}',
			    read_at       TIMESTAMPTZ,
			    dismissed_at  TIMESTAMPTZ,
			    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
			)
		`, schema),
		fmt.Sprintf(`
			CREATE INDEX IF NOT EXISTS idx_notifications_user
			    ON %s.notifications (tenant_id, user_id, created_at DESC)
			    WHERE dismissed_at IS NULL
		`, schema),
		fmt.Sprintf(`
			CREATE INDEX IF NOT EXISTS idx_notifications_unread
			    ON %s.notifications (tenant_id, user_id)
			    WHERE read_at IS NULL AND dismissed_at IS NULL
		`, schema),
	})
}

// bootstrap runs stmts in one transaction under an advisory lock on lockName.
func (s *Store) bootstrap(ctx context.Context, lockName string, stmts []string) error {
	keys := []int64{db.AdvisoryLockKey(lockName)}
	return db.WithAdvisoryLock(ctx, s.db, keys, func(tx *sql.Tx) error {
		for _, stmt := range stmts {
			if _, err := tx.ExecContext(ctx, stmt); err != nil {
				return fmt.Errorf("%s: %w", lockName, err)
			}
		}
		return nil
	})
}
