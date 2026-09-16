// Package savedfilters is the per-tenant-schema saved_filters table — a
// user's bookmarked filter/sort/group-by query string for a list view
// (multitenancy-internals.md §3, view-system.md §4 "Saved filters").
// Engine-owned rather than a module model, since view chrome has no
// module owner. Store also provides the CRUD the built-in
// /_meta/saved-filters endpoint (internal/engine's
// dispatchSavedFiltersCreateRoute/ListRoute/UpdateRoute/DeleteRoute)
// reads and writes through.
package savedfilters

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/db"
	"github.com/djangbahevans/goerp/internal/engine/tenantschema"
)

type Store struct {
	db *sql.DB
}

func NewStore(db *sql.DB) *Store {
	return &Store{db: db}
}

var ErrNotFound = errors.New("saved filter not found")

// SavedFilter is one saved_filters row.
type SavedFilter struct {
	ID          string
	UserID      string
	ViewName    string
	Label       string
	QueryString string
	IsDefault   bool
	CreatedAt   time.Time
}

// Create inserts a new saved filter and returns the created row.
func (s *Store) Create(ctx context.Context, tenantSlug, userID, viewName, label, queryString string, isDefault bool) (*SavedFilter, error) {
	schema := tenantschema.Name(tenantSlug)

	// Transactional so a fresh default's insert and the clearing of any
	// prior default land atomically — otherwise a failure between the two
	// statements can leave two rows is_default=true for the same
	// (user, view), which resolveDefaultSavedFilterState's .find() would
	// then resolve arbitrarily.
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin create saved filter: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	query := fmt.Sprintf(`
		INSERT INTO %s.saved_filters (user_id, view_name, label, query_string, is_default)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id, user_id, view_name, label, query_string, is_default, created_at
	`, schema)

	sf, err := scanSavedFilter(tx.QueryRowContext(ctx, query, userID, viewName, label, queryString, isDefault))
	if err != nil {
		return nil, fmt.Errorf("create saved filter: %w", err)
	}
	if isDefault {
		if err := clearOtherDefaults(ctx, tx, schema, sf.ID, userID, viewName); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit create saved filter: %w", err)
	}
	return sf, nil
}

// ListForUserAndView returns userID's saved filters for viewName, most
// recently created first — enforces own-rows-only at the query itself
// (multitenancy-internals.md's "a user sees and manages only their own
// rows" note), not a separate authorization layer.
func (s *Store) ListForUserAndView(ctx context.Context, tenantSlug, userID, viewName string) ([]SavedFilter, error) {
	schema := tenantschema.Name(tenantSlug)
	query := fmt.Sprintf(`
		SELECT id, user_id, view_name, label, query_string, is_default, created_at
		FROM %s.saved_filters
		WHERE user_id = $1 AND view_name = $2
		ORDER BY created_at DESC
	`, schema)

	rows, err := s.db.QueryContext(ctx, query, userID, viewName)
	if err != nil {
		return nil, fmt.Errorf("list saved filters: %w", err)
	}
	defer rows.Close()

	filters := []SavedFilter{}
	for rows.Next() {
		sf, err := scanSavedFilter(rows)
		if err != nil {
			return nil, fmt.Errorf("list saved filters: %w", err)
		}
		filters = append(filters, *sf)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list saved filters: %w", err)
	}
	return filters, nil
}

// Get returns the saved filter with the given id. Returns ErrNotFound if
// no row matched — used by PATCH/DELETE to resolve the row's owner
// before capping either to its own creator.
func (s *Store) Get(ctx context.Context, tenantSlug, id string) (*SavedFilter, error) {
	schema := tenantschema.Name(tenantSlug)
	query := fmt.Sprintf(`
		SELECT id, user_id, view_name, label, query_string, is_default, created_at
		FROM %s.saved_filters
		WHERE id = $1
	`, schema)

	sf, err := scanSavedFilter(s.db.QueryRowContext(ctx, query, id))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("get saved filter: %w", err)
	}
	return sf, nil
}

// Update renames the filter and/or toggles its default flag —
// query_string is immutable (view-system.md §4: "delete and re-save
// instead"), so neither parameter accepts it. A nil label or isDefault
// leaves that field unchanged. Setting isDefault true clears any other
// default the same user has on the same view, so "the user's own
// is_default saved filter for this view" (view-system.md §4's precedence
// rule) always names at most one row.
func (s *Store) Update(ctx context.Context, tenantSlug, id string, label *string, isDefault *bool) (*SavedFilter, error) {
	schema := tenantschema.Name(tenantSlug)

	// Transactional for the same reason Create is — promoting this row to
	// default and clearing any prior one must land atomically.
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin update saved filter: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	query := fmt.Sprintf(`
		UPDATE %s.saved_filters
		SET label = COALESCE($2, label), is_default = COALESCE($3, is_default)
		WHERE id = $1
		RETURNING id, user_id, view_name, label, query_string, is_default, created_at
	`, schema)

	sf, err := scanSavedFilter(tx.QueryRowContext(ctx, query, id, label, isDefault))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("update saved filter: %w", err)
	}
	if isDefault != nil && *isDefault {
		if err := clearOtherDefaults(ctx, tx, schema, sf.ID, sf.UserID, sf.ViewName); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit update saved filter: %w", err)
	}
	return sf, nil
}

// execer is satisfied by both *sql.DB and *sql.Tx — clearOtherDefaults
// runs inside whichever transaction Create/Update already opened, rather
// than as an unguarded second statement against the pool.
type execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// clearOtherDefaults unsets is_default on every other saved filter the
// same user has on the same view — called after a Create/Update leaves
// exactly one row's is_default true, so a later reader never has to
// pick among several.
func clearOtherDefaults(ctx context.Context, exec execer, schema, keepID, userID, viewName string) error {
	query := fmt.Sprintf(`
		UPDATE %s.saved_filters
		SET is_default = false
		WHERE id != $1 AND user_id = $2 AND view_name = $3 AND is_default = true
	`, schema)
	if _, err := exec.ExecContext(ctx, query, keepID, userID, viewName); err != nil {
		return fmt.Errorf("clear other defaults: %w", err)
	}
	return nil
}

// Delete removes a saved filter by id. Returns ErrNotFound if no row
// matched.
func (s *Store) Delete(ctx context.Context, tenantSlug, id string) error {
	schema := tenantschema.Name(tenantSlug)
	query := fmt.Sprintf(`DELETE FROM %s.saved_filters WHERE id = $1`, schema)

	res, err := s.db.ExecContext(ctx, query, id)
	if err != nil {
		return fmt.Errorf("delete saved filter: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("delete saved filter: %w", err)
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanSavedFilter(sc rowScanner) (*SavedFilter, error) {
	var sf SavedFilter
	if err := sc.Scan(&sf.ID, &sf.UserID, &sf.ViewName, &sf.Label, &sf.QueryString, &sf.IsDefault, &sf.CreatedAt); err != nil {
		return nil, err
	}
	return &sf, nil
}

// Bootstrap creates saved_filters in the given tenant's schema if it
// doesn't already exist. Does not create the schema itself — assumes
// tenant_{slug} already exists (production: tenant provisioning's job;
// this package's own tests create a fixture schema directly). user_id is
// a plain UUID column with no FK, the same "no cross-schema FK,
// validated by the engine at assignment time" reasoning
// recordshares.Store.Bootstrap's own doc comment gives for
// shared_with_user_id/shared_by — system.users lives outside
// tenant_{slug}. Concurrent-safe against other calls racing to bootstrap
// the same tenant's schema (goerp#171) via db.WithAdvisoryLock, scoped to
// tenantSlug, the same way recordshares.Store.Bootstrap is.
func (s *Store) Bootstrap(ctx context.Context, tenantSlug string) error {
	keys := []int64{db.AdvisoryLockKey("savedfilters.Bootstrap:" + tenantSlug)}
	return db.WithAdvisoryLock(ctx, s.db, keys, func(tx *sql.Tx) error {
		schema := tenantschema.Name(tenantSlug)

		createTable := fmt.Sprintf(`
			CREATE TABLE IF NOT EXISTS %s.saved_filters (
			    id           UUID PRIMARY KEY DEFAULT uuidv7(),
			    user_id      UUID NOT NULL,
			    view_name    TEXT NOT NULL,
			    label        TEXT NOT NULL,
			    query_string TEXT NOT NULL,
			    is_default   BOOLEAN NOT NULL DEFAULT FALSE,
			    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
			)
		`, schema)
		if _, err := tx.ExecContext(ctx, createTable); err != nil {
			return fmt.Errorf("create saved_filters table: %w", err)
		}

		// Covers both GET /_meta/saved-filters's own-rows-and-view lookup
		// and clearOtherDefaults' identical (user_id, view_name) scan.
		createIndex := fmt.Sprintf(`
			CREATE INDEX IF NOT EXISTS idx_saved_filters_lookup
			    ON %s.saved_filters(user_id, view_name)
		`, schema)
		if _, err := tx.ExecContext(ctx, createIndex); err != nil {
			return fmt.Errorf("create saved_filters lookup index: %w", err)
		}

		return nil
	})
}
