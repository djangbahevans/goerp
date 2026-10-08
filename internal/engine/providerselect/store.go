// Package providerselect resolves a tenant's single-active connector provider. Explicit
// selections win; a sole enabled provider is the fallback. Multi-active categories require
// callers to name a provider.
package providerselect

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"

	"github.com/rs/zerolog/log"

	abiv1 "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/djangbahevans/goerp/internal/engine/db"
)

// Provider categories a connector manifest can declare under `provides`.
const (
	CategorySMS     = "sms_provider"
	CategoryPush    = "push_provider"
	CategoryOAuth   = "oauth_provider"
	CategoryPayment = "payment_provider"
)

// singleActiveCategories are the categories with one active provider per
// tenant — the only ones Resolve and SetPrimary accept.
var singleActiveCategories = []string{CategorySMS, CategoryPush, CategoryOAuth}

func isSingleActive(category string) bool {
	return slices.Contains(singleActiveCategories, category)
}

// IsCategory reports whether category is one of the provider categories a
// connector manifest can declare, single- or multi-active.
func IsCategory(category string) bool {
	return IsMultiActive(category) || isSingleActive(category)
}

// IsMultiActive reports whether category is multi-active: its callers must
// always name the target module, since Resolve refuses it.
func IsMultiActive(category string) bool {
	return category == CategoryPayment
}

// selected_by is SET NULL on user deletion: a selection outlives the admin
// who made it.
const createTenantProviderSelectionsTable = `
CREATE TABLE IF NOT EXISTS system.tenant_provider_selections (
    tenant_id    UUID NOT NULL REFERENCES system.tenants(id) ON DELETE CASCADE,
    category     TEXT NOT NULL CHECK (category IN ('sms_provider', 'push_provider', 'oauth_provider')),
    module_name  TEXT NOT NULL,
    selected_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    selected_by  UUID REFERENCES system.users(id) ON DELETE SET NULL,
    PRIMARY KEY (tenant_id, category)
)
`

var (
	// ErrNoProviderSelected: more than one enabled module provides the
	// category and none is the tenant's primary.
	ErrNoProviderSelected = errors.New(abiv1.ErrCodeJobsNoProviderSelected)
	// ErrNoProviderInstalled: no enabled module provides the category.
	ErrNoProviderInstalled = errors.New(abiv1.ErrCodeJobsNoProviderInstalled)
	// ErrNotSingleActive: the category isn't one Resolve or SetPrimary
	// handles — payment_provider, or not a provider category at all.
	ErrNotSingleActive = errors.New("category is not a single-active provider category")
	// ErrModuleNotEnabled: SetPrimary's module isn't installed and enabled
	// for the tenant.
	ErrModuleNotEnabled = errors.New("module is not installed and enabled for this tenant")
	// ErrModuleNotProvider: SetPrimary's module is enabled but declares no
	// recognized provider category.
	ErrModuleNotProvider = errors.New("module does not provide a provider category")
)

type Store struct {
	db *sql.DB
}

func NewStore(db *sql.DB) *Store {
	return &Store{db: db}
}

// Bootstrap creates provider selections after the tenant and user stores. An advisory lock
// serializes concurrent calls.
func (s *Store) Bootstrap(ctx context.Context) error {
	keys := []int64{db.SystemSchemaLockKey, db.AdvisoryLockKey("providerselect.Bootstrap")}
	return db.WithAdvisoryLock(ctx, s.db, keys, func(tx *sql.Tx) error {
		if err := db.EnsureSystemSchema(ctx, tx); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, createTenantProviderSelectionsTable); err != nil {
			return fmt.Errorf("create tenant_provider_selections table: %w", err)
		}
		return nil
	})
}

// SetPrimary makes moduleName the tenant's active provider for the category
// its tenant_module_settings row declares, returning that category. It is
// one INSERT ... ON CONFLICT DO UPDATE, so switching providers never needs
// a separate "clear the old primary" step. selectedBy may be empty.
func (s *Store) SetPrimary(ctx context.Context, tenantID, moduleName, selectedBy string) (string, error) {
	var category string
	err := s.db.QueryRowContext(ctx, `
		INSERT INTO system.tenant_provider_selections (tenant_id, category, module_name, selected_by)
		SELECT tenant_id, provider_category, module_name, NULLIF($3, '')::uuid
		FROM system.tenant_module_settings
		WHERE tenant_id = $1 AND module_name = $2 AND enabled
		  AND provider_category = ANY($4)
		ON CONFLICT (tenant_id, category) DO UPDATE
		SET module_name = EXCLUDED.module_name, selected_at = NOW(), selected_by = EXCLUDED.selected_by
		RETURNING category
	`, tenantID, moduleName, selectedBy, singleActiveCategories).Scan(&category)
	if err == nil {
		return category, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", fmt.Errorf("set primary provider: %w", err)
	}
	return "", s.whyNotSelectable(ctx, tenantID, moduleName)
}

// whyNotSelectable explains why SetPrimary's upsert matched no row.
func (s *Store) whyNotSelectable(ctx context.Context, tenantID, moduleName string) error {
	var enabled bool
	var category sql.NullString
	err := s.db.QueryRowContext(ctx, `
		SELECT enabled, provider_category FROM system.tenant_module_settings
		WHERE tenant_id = $1 AND module_name = $2
	`, tenantID, moduleName).Scan(&enabled, &category)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return ErrModuleNotEnabled
	case err != nil:
		return fmt.Errorf("load module settings: %w", err)
	case !enabled:
		return ErrModuleNotEnabled
	case category.String == CategoryPayment:
		return fmt.Errorf("%w: %s", ErrNotSingleActive, category.String)
	default:
		return ErrModuleNotProvider
	}
}

// Resolve returns the module that handles category for tenantID: the
// tenant's selection when that module is still enabled and still provides
// the category, else the only enabled provider when there is exactly one.
// It fails with ErrNoProviderSelected when several are enabled and none is
// selected, ErrNoProviderInstalled when none is, and ErrNotSingleActive for
// payment_provider or any other category outside singleActiveCategories.
func (s *Store) Resolve(ctx context.Context, tenantID, category string) (string, error) {
	if !isSingleActive(category) {
		return "", fmt.Errorf("%w: %s", ErrNotSingleActive, category)
	}

	var selected string
	err := s.db.QueryRowContext(ctx, `
		SELECT s.module_name
		FROM system.tenant_provider_selections s
		JOIN system.tenant_module_settings tms
		  ON tms.tenant_id = s.tenant_id AND tms.module_name = s.module_name
		WHERE s.tenant_id = $1 AND s.category = $2
		  AND tms.enabled AND tms.provider_category = s.category
	`, tenantID, category).Scan(&selected)
	if err == nil {
		return selected, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", fmt.Errorf("load provider selection: %w", err)
	}

	providers, err := s.EnabledProviders(ctx, tenantID, category)
	if err != nil {
		return "", err
	}
	switch len(providers) {
	case 0:
		return "", fmt.Errorf("%w: %s", ErrNoProviderInstalled, category)
	case 1:
		return providers[0], nil
	default:
		log.Error().Str("tenant_id", tenantID).Str("category", category).Strs("providers", providers).
			Msg("providerselect: multiple providers enabled for category, none selected")
		return "", fmt.Errorf("%w: %s", ErrNoProviderSelected, category)
	}
}

// EnabledProviders lists, by name, the enabled modules for tenantID whose
// provider_category is category. Unlike Resolve it accepts any category,
// so it also serves multi-active payment_provider's "list the live
// options" query (connector-guide.md §7).
func (s *Store) EnabledProviders(ctx context.Context, tenantID, category string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT module_name FROM system.tenant_module_settings
		WHERE tenant_id = $1 AND provider_category = $2 AND enabled
		ORDER BY module_name
	`, tenantID, category)
	if err != nil {
		return nil, fmt.Errorf("list enabled providers: %w", err)
	}
	defer rows.Close()

	var providers []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, fmt.Errorf("scan enabled provider: %w", err)
		}
		providers = append(providers, name)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list enabled providers: %w", err)
	}
	return providers, nil
}

// IsEnabledProvider reports whether moduleName is installed and enabled for
// tenantID with category as its provider_category — the check an
// explicitly targeted provider job (connector-guide.md §7 "Multi-active
// categories") gets in place of Resolve.
func (s *Store) IsEnabledProvider(ctx context.Context, tenantID, moduleName, category string) (bool, error) {
	var ok bool
	err := s.db.QueryRowContext(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM system.tenant_module_settings
			WHERE tenant_id = $1 AND module_name = $2 AND provider_category = $3 AND enabled
		)
	`, tenantID, moduleName, category).Scan(&ok)
	if err != nil {
		return false, fmt.Errorf("check enabled provider: %w", err)
	}
	return ok, nil
}
