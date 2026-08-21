// Package files is the per-tenant-schema files table from
// object-storage-guide.md §2 ("The engine creates and maintains the files
// table in every tenant schema"). Lives in each tenant's own
// tenant_{slug} schema, one physical copy per tenant, alongside
// roles/role_permissions/user_roles (internal/engine/role) and
// tenant_invitations (internal/engine/invite) — same Store.Bootstrap(ctx,
// tenantSlug) convention, not internal/engine/tenant's/user's/auditlog's
// no-arg system-schema Bootstrap. Scoped to the row host.storage.upload
// writes (internal/engine/wasm/host_storage.go) — signed-URL issuance,
// soft-delete, and virus-scan status updates are separate, later scope.
package files

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

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

// Bootstrap creates the files table (and its indexes) in the given
// tenant's schema if they don't already exist. Does not create the schema
// itself — assumes tenant_{slug} already exists (production: tenant
// provisioning's job; this package's own tests create a fixture schema
// directly). Concurrent-safe against other calls racing to bootstrap the
// same tenant's schema, scoped to tenantSlug, via db.WithAdvisoryLock —
// same convention as role.Store.Bootstrap/invite.Store.Bootstrap.
func (s *Store) Bootstrap(ctx context.Context, tenantSlug string) error {
	keys := []int64{db.AdvisoryLockKey("files.Bootstrap:" + tenantSlug)}
	return db.WithAdvisoryLock(ctx, s.db, keys, func(tx *sql.Tx) error {
		schema := tenantschema.Name(tenantSlug)

		// id is assigned by the caller (uuid.NewV7(), not DEFAULT
		// uuidv7()) — host.storage.upload needs the file's ID before the
		// insert, to embed it in the object storage key
		// (object-storage-guide.md §12). uploaded_by is a plain UUID
		// column with no FK, same convention as tenant_invitations.invited_by
		// (multitenancy-internals.md §3) — no cross-schema FK to
		// system.users.
		createFiles := fmt.Sprintf(`
			CREATE TABLE IF NOT EXISTS %s.files (
			    id              UUID PRIMARY KEY,
			    tenant_id       UUID NOT NULL,
			    storage_key     TEXT NOT NULL UNIQUE,
			    original_name   TEXT NOT NULL,
			    content_type    TEXT NOT NULL,
			    size_bytes      BIGINT NOT NULL,
			    checksum_sha256 TEXT NOT NULL,
			    uploaded_by     UUID,
			    purpose         TEXT NOT NULL,
			    is_public       BOOLEAN NOT NULL DEFAULT FALSE,
			    scan_status     TEXT NOT NULL DEFAULT 'pending'
			                        CHECK (scan_status IN ('pending', 'clean', 'quarantined', 'skipped')),
			    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			    deleted_at      TIMESTAMPTZ
			)
		`, schema)
		if _, err := tx.ExecContext(ctx, createFiles); err != nil {
			return fmt.Errorf("create files table: %w", err)
		}

		createTenantIndex := fmt.Sprintf(`
			CREATE INDEX IF NOT EXISTS idx_files_tenant ON %s.files (tenant_id) WHERE deleted_at IS NULL
		`, schema)
		if _, err := tx.ExecContext(ctx, createTenantIndex); err != nil {
			return fmt.Errorf("create files tenant index: %w", err)
		}

		createUploadedByIndex := fmt.Sprintf(`
			CREATE INDEX IF NOT EXISTS idx_files_uploaded_by ON %s.files (uploaded_by) WHERE deleted_at IS NULL
		`, schema)
		if _, err := tx.ExecContext(ctx, createUploadedByIndex); err != nil {
			return fmt.Errorf("create files uploaded_by index: %w", err)
		}

		return nil
	})
}

// InsertRow is one files row to write. UploadedBy is "" when the
// uploading request has no user in context (stored as SQL NULL, not the
// empty string — same convention auditlog.Row's optional fields use).
type InsertRow struct {
	ID             string
	TenantID       string
	StorageKey     string
	OriginalName   string
	ContentType    string
	SizeBytes      int64
	ChecksumSHA256 string
	UploadedBy     string
	Purpose        string
	IsPublic       bool
}

// Insert writes row into the given tenant's files table. A plain
// schema-qualified INSERT against the pool — no wrapping transaction, no
// search_path — same lightweight pattern role.go/invite.go use for a
// single tenant-scoped write.
func (s *Store) Insert(ctx context.Context, tenantSlug string, row InsertRow) error {
	schema := tenantschema.Name(tenantSlug)

	var uploadedBy any
	if row.UploadedBy != "" {
		uploadedBy = row.UploadedBy
	}

	query := fmt.Sprintf(`
		INSERT INTO %s.files
		    (id, tenant_id, storage_key, original_name, content_type, size_bytes, checksum_sha256, uploaded_by, purpose, is_public)
		VALUES
		    ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
	`, schema)

	if _, err := s.db.ExecContext(ctx, query,
		row.ID, row.TenantID, row.StorageKey, row.OriginalName, row.ContentType,
		row.SizeBytes, row.ChecksumSHA256, uploadedBy, row.Purpose, row.IsPublic,
	); err != nil {
		return fmt.Errorf("insert files row: %w", err)
	}

	return nil
}

// StorageKeysForTenant returns every storage_key ever recorded for the
// tenant, soft-deleted rows included — offboarding needs every object
// storage key that could still exist under this tenant, not just the ones
// some other consumer still considers live. object-storage-guide.md §12's
// key layout is purpose-first ("{purpose}/{tenant_id}/..."), specifically
// so no single tenant-scoped prefix exists to bulk-delete by; this table
// is the only way to enumerate one tenant's objects across every purpose.
// Returns an empty slice, not an error, if the tenant's files table
// doesn't exist — never bootstrapped, or already dropped by a retried
// offboard job that got past the schema-drop step before crashing.
func (s *Store) StorageKeysForTenant(ctx context.Context, tenantSlug string) ([]string, error) {
	schema := tenantschema.Name(tenantSlug)

	rows, err := s.db.QueryContext(ctx, fmt.Sprintf(`SELECT storage_key FROM %s.files`, schema))
	if err != nil {
		if isUndefinedTable(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("query storage keys for tenant %q: %w", tenantSlug, err)
	}
	defer func() { _ = rows.Close() }()

	var keys []string
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			return nil, fmt.Errorf("scan storage key: %w", err)
		}
		keys = append(keys, key)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate storage keys for tenant %q: %w", tenantSlug, err)
	}

	return keys, nil
}

func isUndefinedTable(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "42P01" // undefined_table
}
