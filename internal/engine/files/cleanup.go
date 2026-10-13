package files

import (
	"context"
	"fmt"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/tenantschema"
)

type DeletedFile struct {
	ID         string
	StorageKey string
}

// DeletedBefore pages retained deletion metadata by ID. An empty afterID starts
// the scan; a missing tenant table has no candidates.
func (s *Store) DeletedBefore(ctx context.Context, tenantSlug string, cutoff time.Time, afterID string, limit int) ([]DeletedFile, error) {
	if limit <= 0 {
		return nil, fmt.Errorf("file cleanup batch limit must be positive")
	}

	query := fmt.Sprintf(`SELECT id, storage_key FROM %s.files
		WHERE deleted_at < $1 AND ($2::uuid IS NULL OR id > $2::uuid)
		ORDER BY id LIMIT $3`, tenantschema.Name(tenantSlug))
	var cursor any
	if afterID != "" {
		cursor = afterID
	}

	rows, err := s.db.QueryContext(ctx, query, cutoff, cursor, limit)
	if err != nil {
		if isUndefinedTable(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("query deleted files: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var candidates []DeletedFile
	for rows.Next() {
		var candidate DeletedFile
		if err := rows.Scan(&candidate.ID, &candidate.StorageKey); err != nil {
			return nil, fmt.Errorf("scan deleted file: %w", err)
		}
		candidates = append(candidates, candidate)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate deleted files: %w", err)
	}

	return candidates, nil
}

// PurgeDeleted removes metadata only while the file still matches the retention
// cutoff and storage key. Callers must delete the backend object first.
func (s *Store) PurgeDeleted(ctx context.Context, tenantSlug string, candidate DeletedFile, cutoff time.Time) error {
	query := fmt.Sprintf(`DELETE FROM %s.files WHERE id = $1 AND storage_key = $2 AND deleted_at < $3`, tenantschema.Name(tenantSlug))
	if _, err := s.db.ExecContext(ctx, query, candidate.ID, candidate.StorageKey, cutoff); err != nil {
		return fmt.Errorf("purge deleted file metadata: %w", err)
	}

	return nil
}
