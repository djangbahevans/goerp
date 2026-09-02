// Package recordshares is the per-tenant-schema record_shares table —
// ad hoc per-record access grants for `.Shareable()` models, independent
// of role/ABAC (multitenancy-internals.md §6, §5a "Document sharing —
// widening the compiled policy"). Engine-owned rather than a module
// model, since it has to apply uniformly across every `.Shareable()`
// model regardless of which module owns it. Store also provides the
// CRUD the built-in `/_meta/shares` endpoint (internal/engine's
// dispatchSharesCreateRoute/dispatchSharesListRoute/
// dispatchSharesDeleteRoute) reads and writes through.
package recordshares

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

var ErrNotFound = errors.New("record share not found")

// Share is one record_shares row.
type Share struct {
	ID               string
	Model            string
	RecordID         string
	SharedWithUserID string
	Permission       string
	SharedBy         string
	CreatedAt        time.Time
	ExpiresAt        *time.Time
}

// Create inserts a new grant and returns the created row.
func (s *Store) Create(ctx context.Context, tenantSlug, model, recordID, sharedWithUserID, permission, sharedBy string, expiresAt *time.Time) (*Share, error) {
	schema := tenantschema.Name(tenantSlug)
	query := fmt.Sprintf(`
		INSERT INTO %s.record_shares (model, record_id, shared_with_user_id, permission, shared_by, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING id, model, record_id, shared_with_user_id, permission, shared_by, created_at, expires_at
	`, schema)

	row := s.db.QueryRowContext(ctx, query, model, recordID, sharedWithUserID, permission, sharedBy, expiresAt)
	sh, err := scanShare(row)
	if err != nil {
		return nil, fmt.Errorf("create record share: %w", err)
	}
	return sh, nil
}

// ListForRecord returns every non-expired grant on (model, recordID),
// most recently created first.
func (s *Store) ListForRecord(ctx context.Context, tenantSlug, model, recordID string) ([]Share, error) {
	schema := tenantschema.Name(tenantSlug)
	query := fmt.Sprintf(`
		SELECT id, model, record_id, shared_with_user_id, permission, shared_by, created_at, expires_at
		FROM %s.record_shares
		WHERE model = $1 AND record_id = $2 AND (expires_at IS NULL OR expires_at > NOW())
		ORDER BY created_at DESC
	`, schema)

	rows, err := s.db.QueryContext(ctx, query, model, recordID)
	if err != nil {
		return nil, fmt.Errorf("list record shares: %w", err)
	}
	defer rows.Close()

	shares := []Share{}
	for rows.Next() {
		sh, err := scanShare(rows)
		if err != nil {
			return nil, fmt.Errorf("list record shares: %w", err)
		}
		shares = append(shares, *sh)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list record shares: %w", err)
	}
	return shares, nil
}

// Get returns the share with the given id. Returns ErrNotFound if no row
// matched — used by DELETE /_meta/shares/{id} to resolve which
// (model, record_id) a revoke targets before capping-checking it.
func (s *Store) Get(ctx context.Context, tenantSlug, id string) (*Share, error) {
	schema := tenantschema.Name(tenantSlug)
	query := fmt.Sprintf(`
		SELECT id, model, record_id, shared_with_user_id, permission, shared_by, created_at, expires_at
		FROM %s.record_shares
		WHERE id = $1
	`, schema)

	sh, err := scanShare(s.db.QueryRowContext(ctx, query, id))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("get record share: %w", err)
	}
	return sh, nil
}

// Delete revokes a grant by id. Returns ErrNotFound if no row matched.
func (s *Store) Delete(ctx context.Context, tenantSlug, id string) error {
	schema := tenantschema.Name(tenantSlug)
	query := fmt.Sprintf(`DELETE FROM %s.record_shares WHERE id = $1`, schema)

	res, err := s.db.ExecContext(ctx, query, id)
	if err != nil {
		return fmt.Errorf("delete record share: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("delete record share: %w", err)
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanShare(sc rowScanner) (*Share, error) {
	var sh Share
	if err := sc.Scan(&sh.ID, &sh.Model, &sh.RecordID, &sh.SharedWithUserID, &sh.Permission, &sh.SharedBy, &sh.CreatedAt, &sh.ExpiresAt); err != nil {
		return nil, err
	}
	return &sh, nil
}

// Bootstrap creates record_shares in the given tenant's schema if it
// doesn't already exist. Does not create the schema itself — assumes
// tenant_{slug} already exists (production: tenant provisioning's job;
// this package's own tests create a fixture schema directly).
// shared_with_user_id and shared_by are plain UUID columns with no FK,
// the same "no cross-schema FK, validated by the engine at assignment
// time" reasoning role.Store.Bootstrap's own doc comment gives for
// user_roles.user_id — system.users lives outside tenant_{slug}.
// Concurrent-safe against other calls racing to bootstrap the same
// tenant's schema (goerp#171) via db.WithAdvisoryLock, scoped to
// tenantSlug, the same way role.Store.Bootstrap is.
func (s *Store) Bootstrap(ctx context.Context, tenantSlug string) error {
	keys := []int64{db.AdvisoryLockKey("recordshares.Bootstrap:" + tenantSlug)}
	return db.WithAdvisoryLock(ctx, s.db, keys, func(tx *sql.Tx) error {
		schema := tenantschema.Name(tenantSlug)

		createTable := fmt.Sprintf(`
			CREATE TABLE IF NOT EXISTS %s.record_shares (
			    id                   UUID PRIMARY KEY DEFAULT uuidv7(),
			    model                TEXT NOT NULL,
			    record_id            UUID NOT NULL,
			    shared_with_user_id  UUID NOT NULL,
			    permission           TEXT NOT NULL CHECK (permission IN ('read', 'write')),
			    shared_by            UUID NOT NULL,
			    created_at           TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			    expires_at           TIMESTAMPTZ
			)
		`, schema)
		if _, err := tx.ExecContext(ctx, createTable); err != nil {
			return fmt.Errorf("create record_shares table: %w", err)
		}

		// Covers the compiled RLS policy's OR EXISTS lookup
		// (multitenancy-internals.md §5a), which filters on exactly
		// these three columns on every read of a .Shareable() model's
		// table — a sequential scan here runs on every such read.
		createIndex := fmt.Sprintf(`
			CREATE INDEX IF NOT EXISTS idx_record_shares_lookup
			    ON %s.record_shares(model, record_id, shared_with_user_id)
		`, schema)
		if _, err := tx.ExecContext(ctx, createIndex); err != nil {
			return fmt.Errorf("create record_shares lookup index: %w", err)
		}

		return nil
	})
}
