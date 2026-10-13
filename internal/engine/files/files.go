// Package files manages each tenant's file metadata table, including rows shared by
// browser and WASM storage uploads.
package files

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

var ErrFileNotFound = errors.New("file not found")

type Store struct {
	db *sql.DB
}

func NewStore(db *sql.DB) *Store {
	return &Store{db: db}
}

// Bootstrap serializes table creation for a tenant using an advisory lock.
// The tenant schema must already exist.
func (s *Store) Bootstrap(ctx context.Context, tenantSlug string) error {
	keys := []int64{db.AdvisoryLockKey("files.Bootstrap:" + tenantSlug)}
	return db.WithAdvisoryLock(ctx, s.db, keys, func(tx *sql.Tx) error {
		schema := tenantschema.Name(tenantSlug)

		// Caller-assigned IDs let uploads include the ID in the storage key.
		// Uploader identities have no cross-schema foreign key to system.users.
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

type InsertRow struct {
	ID             string
	TenantID       string
	StorageKey     string
	OriginalName   string
	ContentType    string
	SizeBytes      int64
	ChecksumSHA256 string
	// Anonymous uploads persist an empty UploadedBy as SQL NULL.
	UploadedBy string
	Purpose    string
	IsPublic   bool
}

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

// MarkDeleted retains the backend object and silently accepts missing or deleted IDs.
func (s *Store) MarkDeleted(ctx context.Context, tenantSlug, id string) error {
	schema := tenantschema.Name(tenantSlug)
	_, err := s.db.ExecContext(ctx, fmt.Sprintf(`UPDATE %s.files SET deleted_at = NOW() WHERE id = $1 AND deleted_at IS NULL`, schema), id)
	if err != nil {
		return fmt.Errorf("mark file %q deleted: %w", id, err)
	}
	return nil
}

type File struct {
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
	ScanStatus     string
	CreatedAt      time.Time
	DeletedAt      *time.Time
}

const fileColumns = `id, tenant_id, storage_key, original_name, content_type, size_bytes, checksum_sha256, uploaded_by, purpose, is_public, scan_status, created_at, deleted_at`

// GetByID includes soft-deleted rows for internal lifecycle operations. A missing
// tenant table returns ErrFileNotFound.
func (s *Store) GetByID(ctx context.Context, tenantSlug, id string) (*File, error) {
	schema := tenantschema.Name(tenantSlug)

	row := s.db.QueryRowContext(ctx, fmt.Sprintf(`SELECT %s FROM %s.files WHERE id = $1`, fileColumns, schema), id)

	var f File
	var uploadedBy sql.NullString
	if err := row.Scan(
		&f.ID, &f.TenantID, &f.StorageKey, &f.OriginalName, &f.ContentType, &f.SizeBytes,
		&f.ChecksumSHA256, &uploadedBy, &f.Purpose, &f.IsPublic, &f.ScanStatus, &f.CreatedAt, &f.DeletedAt,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) || isUndefinedTable(err) {
			return nil, ErrFileNotFound
		}
		return nil, fmt.Errorf("get file %q: %w", id, err)
	}
	if uploadedBy.Valid {
		f.UploadedBy = uploadedBy.String
	}

	return &f, nil
}

// StorageKeysForTenant includes soft-deleted files for complete offboarding. Purpose-first
// storage keys prevent deletion by one tenant prefix; a missing files table returns an
// empty list for retries.
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
	pgErr, ok := errors.AsType[*pgconn.PgError](err)
	return ok && pgErr.Code == "42P01" // undefined_table
}

// DeleteActive atomically soft-deletes an active file owned by tenantID.
// Missing and already-deleted files return ErrFileNotFound.
func (s *Store) DeleteActive(ctx context.Context, tenantSlug, tenantID, id string) error {
	schema := tenantschema.Name(tenantSlug)
	result, err := s.db.ExecContext(ctx, fmt.Sprintf(`UPDATE %s.files SET deleted_at = NOW() WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL`, schema), id, tenantID)
	if err != nil {
		if isUndefinedTable(err) {
			return ErrFileNotFound
		}
		return fmt.Errorf("soft-delete file: %w", err)
	}

	count, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("count deleted files: %w", err)
	}
	if count == 0 {
		return ErrFileNotFound
	}

	return nil
}
