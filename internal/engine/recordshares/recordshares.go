// Package recordshares is the per-tenant-schema record_shares table —
// ad hoc per-record access grants for `.Shareable()` models, independent
// of role/ABAC (multitenancy-internals.md §6, §5a "Document sharing —
// widening the compiled policy"). Engine-owned rather than a module
// model, since it has to apply uniformly across every `.Shareable()`
// model regardless of which module owns it. Scoped to table creation
// only — the built-in `/_meta/shares` endpoint that reads and writes it
// is separate, larger scope.
package recordshares

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/djangbahevans/goerp/internal/engine/db"
	"github.com/djangbahevans/goerp/internal/engine/tenantschema"
)

type Store struct {
	db *sql.DB
}

func NewStore(db *sql.DB) *Store {
	return &Store{db: db}
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
