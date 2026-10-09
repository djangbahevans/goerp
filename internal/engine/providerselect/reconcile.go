package providerselect

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
)

func Categories(provides map[string]bool) []string {
	categories := []string{}
	for category, provided := range provides {
		if provided && IsCategory(category) {
			categories = append(categories, category)
		}
	}

	slices.Sort(categories)

	return categories
}

// Reconcile locks settings to preserve tenant choices during eligibility refreshes.
// Nil categories remove eligibility without creating a non-connector setting.
func (s *Store) Reconcile(ctx context.Context, tenantID, moduleName string, categories []string) error {
	for _, category := range categories {
		if !IsCategory(category) {
			return fmt.Errorf("reconcile provider eligibility: invalid category %q", category)
		}
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin provider eligibility reconciliation: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if categories != nil {
		_, err = tx.ExecContext(ctx, `
			INSERT INTO system.tenant_module_settings (tenant_id, module_name)
			VALUES ($1, $2)
			ON CONFLICT (tenant_id, module_name) DO UPDATE SET module_name = EXCLUDED.module_name
		`, tenantID, moduleName)
	} else {
		var exists int
		err = tx.QueryRowContext(ctx, `SELECT 1 FROM system.tenant_module_settings
			WHERE tenant_id = $1 AND module_name = $2 FOR UPDATE`, tenantID, moduleName).Scan(&exists)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
	}
	if err != nil {
		return fmt.Errorf("lock provider module setting: %w", err)
	}

	if _, err := tx.ExecContext(ctx, `DELETE FROM system.tenant_module_provider_categories
		WHERE tenant_id = $1 AND module_name = $2 AND NOT (category = ANY(COALESCE($3::text[], '{}'::text[])))`, tenantID, moduleName, categories); err != nil {
		return fmt.Errorf("remove obsolete provider categories: %w", err)
	}

	if len(categories) > 0 {
		if _, err := tx.ExecContext(ctx, `INSERT INTO system.tenant_module_provider_categories (tenant_id, module_name, category)
			SELECT $1, $2, unnest($3::text[]) ON CONFLICT DO NOTHING`, tenantID, moduleName, categories); err != nil {
			return fmt.Errorf("record provider categories: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit provider eligibility reconciliation: %w", err)
	}

	return nil
}
