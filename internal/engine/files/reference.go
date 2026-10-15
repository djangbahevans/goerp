package files

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/djangbahevans/goerp/internal/engine/tenantschema"
)

// ActiveReference uses the caller's transaction to resolve a live tenant file.
func ActiveReference(ctx context.Context, tx *sql.Tx, tenantSlug, tenantID, id string) (*File, error) {
	query := fmt.Sprintf(`SELECT id, original_name, content_type, size_bytes, storage_key, is_public
		FROM %s.files WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL AND scan_status <> 'quarantined'`, tenantschema.Name(tenantSlug))

	var file File
	err := tx.QueryRowContext(ctx, query, id, tenantID).Scan(&file.ID, &file.OriginalName, &file.ContentType, &file.SizeBytes, &file.StorageKey, &file.IsPublic)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrFileNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("resolve tenant file: %w", err)
	}

	return new(file), nil
}
