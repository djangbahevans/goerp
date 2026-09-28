// Package activitytype is the per-tenant-schema activity_types table — the
// tenant's own set of scheduled-activity types, which
// scheduled_activities.type references (scheduled-activities.md §9).
// Engine-owned: only the engine writes it, and host.db rejects module SQL
// that names it. Store provides the reads and writes the built-in
// /_meta/activity-types and /admin/activity-types endpoints go through.
package activitytype

import (
	"context"
	"database/sql"
	"encoding/json/v2"
	"errors"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/db"
	"github.com/djangbahevans/goerp/internal/engine/l10n"
	"github.com/djangbahevans/goerp/internal/engine/tenantschema"
	"github.com/jackc/pgx/v5/pgconn"
)

// TableName is the table's unqualified name, as module SQL would spell it.
const TableName = "activity_types"

// MaxTypes is how many types a tenant can have, archived ones included.
const MaxTypes = 50

// KeyPattern is a type key's shape; the table's CHECK enforces the same.
var KeyPattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,39}$`)

var (
	ErrNotFound = errors.New("activity type not found")
	// ErrKeyTaken is returned by Create for a key that already exists,
	// archived included.
	ErrKeyTaken = errors.New("activity type key already exists")
	// ErrLimitReached is returned by Create when the tenant already has
	// MaxTypes types.
	ErrLimitReached = errors.New("activity type limit reached")
	// ErrLastActive is returned by a write that would leave the tenant
	// with no active type.
	ErrLastActive = errors.New("the last active activity type can't be archived or deleted")
	// ErrInUse is returned by Delete for a type some activity uses.
	ErrInUse = errors.New("activity type is in use")
	// ErrOrderMismatch is returned by Reorder when the keys aren't exactly
	// the tenant's keys.
	ErrOrderMismatch = errors.New("order must list every activity type key exactly once")
)

type Store struct {
	db *sql.DB
}

func NewStore(db *sql.DB) *Store {
	return &Store{db: db}
}

// Type is one activity_types row. Label and DefaultSummary map locale
// tags to text; DefaultSummary is empty for none. UsageCount is filled
// only by the reads that say so.
type Type struct {
	Key            string
	Label          map[string]string
	Icon           string
	DefaultSummary map[string]string
	DefaultDueDays *int
	Position       int
	ArchivedAt     *time.Time
	CreatedAt      time.Time
	UpdatedAt      time.Time
	UsageCount     int
}

func (t *Type) Archived() bool { return t.ArchivedAt != nil }

const typeColumns = `t.key, t.label, t.icon, t.default_summary, t.default_due_days, t.position, t.archived_at, t.created_at, t.updated_at`

// builtinTypes are the types provisioning seeds, in order, with the
// engine's shipped label translations (scheduled-activities.md §9
// "Built-in types").
var builtinTypes = []struct {
	key    string
	icon   string
	labels map[string]string
}{
	{"call", "phone", map[string]string{"en": "Call", "fr": "Appel", "ar": "مكالمة"}},
	{"meeting", "users", map[string]string{"en": "Meeting", "fr": "Réunion", "ar": "اجتماع"}},
	{"email", "mail", map[string]string{"en": "Email", "fr": "E-mail", "ar": "بريد إلكتروني"}},
	{"todo", "square-check", map[string]string{"en": "To-do", "fr": "À faire", "ar": "مهمة"}},
}

// Resolve returns m's text for locale: the translation fallback chain
// (exact locale, its language, the platform default — l10n-guide.md §2
// "Translation locale fallback"), then tenantDefault, then the first entry
// by locale tag. ok is false only when m is empty.
func Resolve(m map[string]string, locale, tenantDefault string) (text string, ok bool) {
	candidates := []string{locale}
	if lang, _, found := strings.Cut(locale, "-"); found {
		candidates = append(candidates, lang)
	}
	candidates = append(candidates, l10n.PlatformDefaultLocale, tenantDefault)
	for _, c := range candidates {
		if text, ok := m[c]; ok {
			return text, true
		}
	}
	if len(m) == 0 {
		return "", false
	}
	return m[slices.Min(slices.Collect(maps.Keys(m)))], true
}

// seedLabel is a built-in type's label map for locales: each locale's
// shipped translation by the fallback chain, so a locale the engine has
// no translation for gets its language's or the platform default's.
func seedLabel(shipped map[string]string, locales []string) map[string]string {
	label := map[string]string{}
	for _, locale := range locales {
		label[locale], _ = Resolve(shipped, locale, l10n.PlatformDefaultLocale)
	}
	return label
}

// Bootstrap creates activity_types in the given tenant's schema if it
// doesn't already exist. Does not create the schema itself, or seed it
// (Seed). Concurrent-safe via db.WithAdvisoryLock.
func (s *Store) Bootstrap(ctx context.Context, tenantSlug string) error {
	keys := []int64{db.AdvisoryLockKey("activitytype.Bootstrap:" + tenantSlug)}
	return db.WithAdvisoryLock(ctx, s.db, keys, func(tx *sql.Tx) error {
		createTable := fmt.Sprintf(`
			CREATE TABLE IF NOT EXISTS %s.activity_types (
			    key              TEXT PRIMARY KEY CHECK (key ~ '^[a-z][a-z0-9_]{0,39}$'),
			    label            JSONB NOT NULL,
			    icon             TEXT NOT NULL,
			    default_summary  JSONB NOT NULL DEFAULT '{}',
			    default_due_days INT CHECK (default_due_days BETWEEN 0 AND 365),
			    position         INT NOT NULL,
			    archived_at      TIMESTAMPTZ,
			    created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			    updated_at       TIMESTAMPTZ NOT NULL DEFAULT NOW()
			)
		`, tenantschema.Name(tenantSlug))
		if _, err := tx.ExecContext(ctx, createTable); err != nil {
			return fmt.Errorf("create activity_types table: %w", err)
		}
		return nil
	})
}

// Seed inserts the built-in types, labelled for every locale in locales
// plus the platform default, when the tenant has no types yet — so a
// retried provisioning step never brings back a type an admin deleted.
func (s *Store) Seed(ctx context.Context, tenantSlug string, locales []string) error {
	locales = append(slices.Clone(locales), l10n.PlatformDefaultLocale)
	return s.withWriteLock(ctx, tenantSlug, func(tx *sql.Tx) error {
		var exists bool
		query := fmt.Sprintf(`SELECT EXISTS (SELECT 1 FROM %s.activity_types)`, tenantschema.Name(tenantSlug))
		if err := tx.QueryRowContext(ctx, query).Scan(&exists); err != nil {
			return fmt.Errorf("seed activity types: %w", err)
		}
		if exists {
			return nil
		}
		insert := fmt.Sprintf(`INSERT INTO %s.activity_types (key, label, icon, position) VALUES ($1, $2::jsonb, $3, $4)`, tenantschema.Name(tenantSlug))
		for i, b := range builtinTypes {
			label, err := json.Marshal(seedLabel(b.labels, locales))
			if err != nil {
				return fmt.Errorf("seed activity types: %w", err)
			}
			if _, err := tx.ExecContext(ctx, insert, b.key, string(label), b.icon, i+1); err != nil {
				return fmt.Errorf("seed activity type %s: %w", b.key, err)
			}
		}
		return nil
	})
}

// withWriteLock runs fn in a transaction holding the tenant's activity
// type write lock, so the limit, last-active and order checks see no
// concurrent write.
func (s *Store) withWriteLock(ctx context.Context, tenantSlug string, fn func(tx *sql.Tx) error) error {
	return db.WithAdvisoryLock(ctx, s.db, []int64{db.AdvisoryLockKey("activitytype.write:" + tenantSlug)}, fn)
}

// List returns every type, active and archived, in position order. With
// usage, each carries its UsageCount.
func (s *Store) List(ctx context.Context, tenantSlug string, usage bool) ([]Type, error) {
	schema := tenantschema.Name(tenantSlug)
	var query string
	if usage {
		query = fmt.Sprintf(`
			SELECT %s, COALESCE(u.n, 0)
			FROM %s.activity_types t
			LEFT JOIN (SELECT type, count(*) AS n FROM %s.scheduled_activities GROUP BY type) u ON u.type = t.key
			ORDER BY t.position, t.key
		`, typeColumns, schema, schema)
	} else {
		query = fmt.Sprintf(`SELECT %s, 0 FROM %s.activity_types t ORDER BY t.position, t.key`, typeColumns, schema)
	}

	rows, err := s.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("list activity types: %w", err)
	}
	defer rows.Close()
	types := []Type{}
	for rows.Next() {
		t, err := scanType(rows)
		if err != nil {
			return nil, fmt.Errorf("list activity types: %w", err)
		}
		types = append(types, *t)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list activity types: %w", err)
	}
	return types, nil
}

// Get returns the type with key and its UsageCount, or ErrNotFound.
func (s *Store) Get(ctx context.Context, tenantSlug, key string) (*Type, error) {
	return getType(ctx, s.db, tenantSlug, key, "")
}

type queryer interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// getType reads key with its UsageCount; lock is appended to the query
// (e.g. "FOR UPDATE OF t").
func getType(ctx context.Context, q queryer, tenantSlug, key, lock string) (*Type, error) {
	schema := tenantschema.Name(tenantSlug)
	query := fmt.Sprintf(`
		SELECT %s, (SELECT count(*) FROM %s.scheduled_activities WHERE type = t.key)
		FROM %s.activity_types t WHERE t.key = $1 %s
	`, typeColumns, schema, schema, lock)
	t, err := scanType(q.QueryRowContext(ctx, query, key))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get activity type: %w", err)
	}
	return t, nil
}

// IsActive reports whether key is one of the tenant's types and not
// archived.
func (s *Store) IsActive(ctx context.Context, tenantSlug, key string) (bool, error) {
	var active bool
	query := fmt.Sprintf(`SELECT EXISTS (SELECT 1 FROM %s.activity_types WHERE key = $1 AND archived_at IS NULL)`, tenantschema.Name(tenantSlug))
	if err := s.db.QueryRowContext(ctx, query, key).Scan(&active); err != nil {
		return false, fmt.Errorf("check activity type: %w", err)
	}
	return active, nil
}

// NewType is the input to Create. A nil DefaultSummary is none.
type NewType struct {
	Key            string
	Label          map[string]string
	Icon           string
	DefaultSummary map[string]string
	DefaultDueDays *int
}

// Create adds a type after every existing one. Returns ErrKeyTaken or
// ErrLimitReached.
func (s *Store) Create(ctx context.Context, tenantSlug string, in NewType) (*Type, error) {
	label, err := json.Marshal(in.Label)
	if err != nil {
		return nil, fmt.Errorf("create activity type: %w", err)
	}
	if in.DefaultSummary == nil {
		in.DefaultSummary = map[string]string{}
	}
	summary, err := json.Marshal(in.DefaultSummary)
	if err != nil {
		return nil, fmt.Errorf("create activity type: %w", err)
	}

	var created *Type
	err = s.withWriteLock(ctx, tenantSlug, func(tx *sql.Tx) error {
		schema := tenantschema.Name(tenantSlug)
		var taken bool
		var count, maxPosition int
		query := fmt.Sprintf(`SELECT bool_or(key = $1) IS TRUE, count(*), COALESCE(max(position), 0) FROM %s.activity_types`, schema)
		if err := tx.QueryRowContext(ctx, query, in.Key).Scan(&taken, &count, &maxPosition); err != nil {
			return fmt.Errorf("create activity type: %w", err)
		}
		switch {
		case taken:
			return ErrKeyTaken
		case count >= MaxTypes:
			return ErrLimitReached
		}

		insert := fmt.Sprintf(`
			INSERT INTO %s.activity_types AS t (key, label, icon, default_summary, default_due_days, position)
			VALUES ($1, $2::jsonb, $3, $4::jsonb, $5, $6)
			RETURNING %s, 0
		`, schema, typeColumns)
		t, err := scanType(tx.QueryRowContext(ctx, insert, in.Key, string(label), in.Icon, string(summary), in.DefaultDueDays, maxPosition+1))
		if err != nil {
			return fmt.Errorf("create activity type: %w", err)
		}
		created = t
		return nil
	})
	if err != nil {
		return nil, err
	}
	return created, nil
}

// Update is the input to Store.Update; a nil field is left unchanged.
// Label and DefaultSummary replace the whole map; an empty DefaultSummary
// clears it. ClearDefaultDueDays sets default_due_days to NULL and takes
// precedence over DefaultDueDays.
type Update struct {
	Label               map[string]string
	Icon                *string
	DefaultSummary      map[string]string
	DefaultDueDays      *int
	ClearDefaultDueDays bool
	Archived            *bool
}

// Update applies u to key and returns the type with its UsageCount.
// Returns ErrNotFound, or ErrLastActive when u would archive the only
// active type.
func (s *Store) Update(ctx context.Context, tenantSlug, key string, u Update) (*Type, error) {
	var label, summary *string
	if u.Label != nil {
		raw, err := json.Marshal(u.Label)
		if err != nil {
			return nil, fmt.Errorf("update activity type: %w", err)
		}
		label = new(string(raw))
	}
	if u.DefaultSummary != nil {
		raw, err := json.Marshal(u.DefaultSummary)
		if err != nil {
			return nil, fmt.Errorf("update activity type: %w", err)
		}
		summary = new(string(raw))
	}

	var updated *Type
	err := s.withWriteLock(ctx, tenantSlug, func(tx *sql.Tx) error {
		current, err := getType(ctx, tx, tenantSlug, key, "FOR UPDATE OF t")
		if err != nil {
			return err
		}
		if u.Archived != nil && *u.Archived && !current.Archived() {
			if err := checkAnotherActive(ctx, tx, tenantSlug, key); err != nil {
				return err
			}
		}

		query := fmt.Sprintf(`
			UPDATE %s.activity_types AS t SET
			    label            = COALESCE($2::jsonb, label),
			    icon             = COALESCE($3, icon),
			    default_summary  = COALESCE($4::jsonb, default_summary),
			    default_due_days = CASE WHEN $5::boolean THEN NULL ELSE COALESCE($6, default_due_days) END,
			    archived_at      = CASE WHEN $7::boolean IS NULL THEN archived_at
			                            WHEN $7::boolean THEN COALESCE(archived_at, NOW())
			                            ELSE NULL END,
			    updated_at       = NOW()
			WHERE key = $1
			RETURNING %s, $8::int
		`, tenantschema.Name(tenantSlug), typeColumns)
		t, err := scanType(tx.QueryRowContext(ctx, query, key, label, u.Icon, summary, u.ClearDefaultDueDays, u.DefaultDueDays, u.Archived, current.UsageCount))
		if err != nil {
			return fmt.Errorf("update activity type: %w", err)
		}
		updated = t
		return nil
	})
	if err != nil {
		return nil, err
	}
	return updated, nil
}

// checkAnotherActive returns ErrLastActive when no active type other than
// key exists.
func checkAnotherActive(ctx context.Context, tx *sql.Tx, tenantSlug, key string) error {
	var other bool
	query := fmt.Sprintf(`SELECT EXISTS (SELECT 1 FROM %s.activity_types WHERE key <> $1 AND archived_at IS NULL)`, tenantschema.Name(tenantSlug))
	if err := tx.QueryRowContext(ctx, query, key).Scan(&other); err != nil {
		return fmt.Errorf("check active activity types: %w", err)
	}
	if !other {
		return ErrLastActive
	}
	return nil
}

// Reorder sets every type's position to its index in keys, which must
// list each of the tenant's keys exactly once (ErrOrderMismatch).
func (s *Store) Reorder(ctx context.Context, tenantSlug string, keys []string) error {
	return s.withWriteLock(ctx, tenantSlug, func(tx *sql.Tx) error {
		schema := tenantschema.Name(tenantSlug)
		rows, err := tx.QueryContext(ctx, fmt.Sprintf(`SELECT key FROM %s.activity_types`, schema))
		if err != nil {
			return fmt.Errorf("reorder activity types: %w", err)
		}
		var current []string
		for rows.Next() {
			var k string
			if err := rows.Scan(&k); err != nil {
				_ = rows.Close()
				return fmt.Errorf("reorder activity types: %w", err)
			}
			current = append(current, k)
		}
		if err := rows.Err(); err != nil {
			return fmt.Errorf("reorder activity types: %w", err)
		}
		sorted := slices.Sorted(slices.Values(keys))
		slices.Sort(current)
		if !slices.Equal(sorted, current) {
			return ErrOrderMismatch
		}

		query := fmt.Sprintf(`
			UPDATE %s.activity_types t SET position = o.n, updated_at = NOW()
			FROM unnest($1::text[]) WITH ORDINALITY AS o(key, n)
			WHERE t.key = o.key AND t.position <> o.n
		`, schema)
		if _, err := tx.ExecContext(ctx, query, keys); err != nil {
			return fmt.Errorf("reorder activity types: %w", err)
		}
		return nil
	})
}

// Delete removes key. Returns ErrNotFound, ErrInUse for a type any
// activity uses, open or done, or ErrLastActive for the only active type.
func (s *Store) Delete(ctx context.Context, tenantSlug, key string) error {
	return s.withWriteLock(ctx, tenantSlug, func(tx *sql.Tx) error {
		t, err := getType(ctx, tx, tenantSlug, key, "FOR UPDATE OF t")
		if err != nil {
			return err
		}
		if t.UsageCount > 0 {
			return ErrInUse
		}
		if !t.Archived() {
			if err := checkAnotherActive(ctx, tx, tenantSlug, key); err != nil {
				return err
			}
		}
		query := fmt.Sprintf(`DELETE FROM %s.activity_types WHERE key = $1`, tenantschema.Name(tenantSlug))
		if _, err := tx.ExecContext(ctx, query, key); err != nil {
			// An activity scheduled with it since the count: the foreign
			// key's ON DELETE RESTRICT refuses.
			if pgErr, ok := errors.AsType[*pgconn.PgError](err); ok && pgErr.Code == "23503" {
				return ErrInUse
			}
			return fmt.Errorf("delete activity type: %w", err)
		}
		return nil
	})
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanType(sc rowScanner) (*Type, error) {
	var t Type
	var label, summary []byte
	var dueDays sql.NullInt32
	if err := sc.Scan(&t.Key, &label, &t.Icon, &summary, &dueDays, &t.Position, &t.ArchivedAt, &t.CreatedAt, &t.UpdatedAt, &t.UsageCount); err != nil {
		return nil, err
	}
	if err := json.Unmarshal(label, &t.Label); err != nil {
		return nil, fmt.Errorf("decode label: %w", err)
	}
	if err := json.Unmarshal(summary, &t.DefaultSummary); err != nil {
		return nil, fmt.Errorf("decode default_summary: %w", err)
	}
	if dueDays.Valid {
		t.DefaultDueDays = new(int(dueDays.Int32))
	}
	return &t, nil
}
