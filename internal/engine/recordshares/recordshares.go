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
	"github.com/jackc/pgx/v5/pgconn"
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

// Grant records that sharedWithUserID may access (model, recordID) at
// permission, and returns the resulting row. A recipient already holding a
// grant on the record has it updated in place — permission, expires_at and
// shared_by are replaced, id and created_at are kept — and created reports
// false; otherwise a row is inserted and created is true. The unique index
// on (model, record_id, shared_with_user_id) makes this race-safe.
func (s *Store) Grant(ctx context.Context, tenantSlug, model, recordID, sharedWithUserID, permission, sharedBy string, expiresAt *time.Time) (sh *Share, created bool, err error) {
	sh, created, err = s.upsert(ctx, tenantSlug, model, recordID, sharedWithUserID, permission, sharedBy, expiresAt)
	if !isMissingConflictTarget(err) {
		return sh, created, err
	}

	// A tenant provisioned before the unique index existed only gains it when
	// Bootstrap or a .Shareable() module's schema sync next runs for it, which
	// may be never; adding it here keeps sharing working on such a tenant.
	if err := s.Bootstrap(ctx, tenantSlug); err != nil {
		return nil, false, fmt.Errorf("add record_shares unique index: %w", err)
	}
	return s.upsert(ctx, tenantSlug, model, recordID, sharedWithUserID, permission, sharedBy, expiresAt)
}

func (s *Store) upsert(ctx context.Context, tenantSlug, model, recordID, sharedWithUserID, permission, sharedBy string, expiresAt *time.Time) (*Share, bool, error) {
	schema := tenantschema.Name(tenantSlug)
	query := fmt.Sprintf(`
		INSERT INTO %s.record_shares (model, record_id, shared_with_user_id, permission, shared_by, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (model, record_id, shared_with_user_id) DO UPDATE
		    SET permission = EXCLUDED.permission, shared_by = EXCLUDED.shared_by, expires_at = EXCLUDED.expires_at
		RETURNING id, model, record_id, shared_with_user_id, permission, shared_by, created_at, expires_at, (xmax = 0)
	`, schema)

	var out Share
	var created bool
	if err := s.db.QueryRowContext(ctx, query, model, recordID, sharedWithUserID, permission, sharedBy, expiresAt).Scan(
		&out.ID, &out.Model, &out.RecordID, &out.SharedWithUserID, &out.Permission, &out.SharedBy, &out.CreatedAt, &out.ExpiresAt, &created,
	); err != nil {
		return nil, false, fmt.Errorf("grant record share: %w", err)
	}
	return &out, created, nil
}

// isMissingConflictTarget reports Postgres's invalid_column_reference error
// (42P10) for an ON CONFLICT target no unique index backs.
func isMissingConflictTarget(err error) bool {
	pgErr, ok := errors.AsType[*pgconn.PgError](err)
	return ok && pgErr.Code == "42P10"
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

		for _, stmt := range UniqueIndexStatements(schema + ".") {
			if _, err := tx.ExecContext(ctx, stmt); err != nil {
				return fmt.Errorf("create record_shares unique index: %w", err)
			}
		}

		return nil
	})
}

// UniqueIndexStatements returns the statements that give record_shares its
// one-row-per-(model, record_id, shared_with_user_id) unique index, for the
// table qualified by schemaPrefix ("tenant_acme." or "" to resolve through
// search_path). Shared by Bootstrap and schema sync's own copy of the DDL so
// the two cannot drift; both run them in a transaction.
//
// On a table without the index — one created before it existed — duplicates
// are removed first, keeping one row per key: a grant that has not expired in
// preference to one that has, then the most recently created. The block runs
// only while the index is absent, so later calls are a no-op. The unique index
// also serves the compiled RLS policy's OR EXISTS lookup (multitenancy-
// internals.md §5a), which filters on exactly these three columns on every
// read of a .Shareable() model's table, so the earlier non-unique lookup index
// is dropped as redundant. The lock timeout makes the DDL fail, and the sync
// retry, rather than queue behind a long-running read and stall every later
// query on the table.
func UniqueIndexStatements(schemaPrefix string) []string {
	return []string{
		`SET LOCAL lock_timeout = '5s'`,
		fmt.Sprintf(`
			DO $$
			BEGIN
			    IF to_regclass('%[1]sidx_record_shares_unique') IS NULL THEN
			        LOCK TABLE %[1]srecord_shares IN SHARE ROW EXCLUSIVE MODE;
			        DELETE FROM %[1]srecord_shares
			        WHERE id IN (
			            SELECT id FROM (
			                SELECT id, ROW_NUMBER() OVER (
			                    PARTITION BY model, record_id, shared_with_user_id
			                    ORDER BY (expires_at IS NULL OR expires_at > NOW()) DESC, created_at DESC, id DESC
			                ) AS rank
			                FROM %[1]srecord_shares
			            ) ranked
			            WHERE rank > 1
			        );
			        CREATE UNIQUE INDEX idx_record_shares_unique
			            ON %[1]srecord_shares(model, record_id, shared_with_user_id);
			    END IF;
			END $$
		`, schemaPrefix),
		fmt.Sprintf(`DROP INDEX IF EXISTS %sidx_record_shares_lookup`, schemaPrefix),
	}
}
