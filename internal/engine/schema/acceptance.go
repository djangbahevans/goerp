package schema

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// RecordAcceptance inserts one system.schema_sync_acceptances row, pinned
// to moduleVersion (the manifest version Accept diffed against — see
// createSchemaSyncAcceptancesTable's own doc comment for why). If an
// unconsumed row already exists for this exact (tenant, module, version,
// hash) — the partial unique index's conflict target — this returns that
// row's id instead of erroring, so two racing `POST /admin/schema/accept`
// calls for the same still-blocked change converge on one acceptance row
// rather than one succeeding and one failing. Returns the row's id,
// goerp#292's `acceptance_id` in `POST /admin/schema/accept`'s
// `202 {job_id, acceptance_id}` response.
func (p *SchemaSyncPool) RecordAcceptance(ctx context.Context, tenantID, moduleName, moduleVersion, targetHash, reason, operator string) (string, error) {
	var id string
	err := p.primary.QueryRowContext(ctx, `
		INSERT INTO system.schema_sync_acceptances (tenant_id, module_name, module_version, target_hash, reason, operator)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (tenant_id, module_name, module_version, target_hash) WHERE consumed_at IS NULL DO NOTHING
		RETURNING id
	`, tenantID, moduleName, moduleVersion, targetHash, reason, operator).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		err = p.primary.QueryRowContext(ctx, `
			SELECT id FROM system.schema_sync_acceptances
			WHERE tenant_id = $1 AND module_name = $2 AND module_version = $3 AND target_hash = $4 AND consumed_at IS NULL
		`, tenantID, moduleName, moduleVersion, targetHash).Scan(&id)
	}
	if err != nil {
		return "", fmt.Errorf("record schema sync acceptance: %w", err)
	}
	return id, nil
}

// AcceptedHashes returns every not-yet-consumed target_hash accepted for
// (tenantID, moduleName) under exactly moduleVersion — the manifest
// version currently loaded, so an acceptance recorded against a since-
// superseded version never matches (see createSchemaSyncAcceptancesTable's
// own doc comment). Excludes any hash apply.go's markAcceptancesConsumed
// has already marked used, in the same transaction as the DDL that
// consumed it.
func (p *SchemaSyncPool) AcceptedHashes(ctx context.Context, tenantID, moduleName, moduleVersion string) (map[string]bool, error) {
	rows, err := p.primary.QueryContext(ctx, `
		SELECT DISTINCT target_hash FROM system.schema_sync_acceptances
		WHERE tenant_id = $1 AND module_name = $2 AND module_version = $3 AND consumed_at IS NULL
	`, tenantID, moduleName, moduleVersion)
	if err != nil {
		return nil, fmt.Errorf("query accepted schema diff hashes: %w", err)
	}
	defer func() { _ = rows.Close() }()

	hashes := make(map[string]bool)
	for rows.Next() {
		var h string
		if err := rows.Scan(&h); err != nil {
			return nil, fmt.Errorf("scan accepted hash: %w", err)
		}
		hashes[h] = true
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate accepted hashes: %w", err)
	}
	return hashes, nil
}
