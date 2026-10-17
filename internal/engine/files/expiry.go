package files

import (
	"context"
	"fmt"

	"github.com/djangbahevans/goerp/internal/engine/tenantschema"
)

const expirySQL = `CASE purpose
	WHEN 'imports' THEN created_at + INTERVAL '720 hours'
	WHEN 'exports' THEN created_at + INTERVAL '168 hours'
	WHEN 'reports' THEN created_at + INTERVAL '168 hours'
	END`

// Expire records purpose deadlines as soft deletions so the ordinary cleanup
// sweep can purge objects even on backends without bucket expiry.
func (s *Store) Expire(ctx context.Context, tenantSlug string) error {
	query := fmt.Sprintf(`UPDATE %s.files SET deleted_at = %s
		WHERE deleted_at IS NULL AND (%s) <= NOW()`, tenantschema.Name(tenantSlug), expirySQL, expirySQL)
	if _, err := s.db.ExecContext(ctx, query); err != nil && !isUndefinedTable(err) {
		return fmt.Errorf("expire tenant files: %w", err)
	}

	return nil
}
