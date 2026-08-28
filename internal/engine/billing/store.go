package billing

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/db"
)

// createPlansTable matches multitenancy-internals.md §2's plans
// definition.
const createPlansTable = `
CREATE TABLE IF NOT EXISTS system.plans (
    id              UUID PRIMARY KEY DEFAULT uuidv7(),
    name            TEXT NOT NULL UNIQUE,
    display_name    TEXT NOT NULL,
    price_monthly   BIGINT,
    price_yearly    BIGINT,
    currency        CHAR(3) NOT NULL DEFAULT 'USD',
    is_active       BOOLEAN NOT NULL DEFAULT TRUE,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
)
`

const createPlanEntitlementsTable = `
CREATE TABLE IF NOT EXISTS system.plan_entitlements (
    plan_id     UUID NOT NULL REFERENCES system.plans(id) ON DELETE CASCADE,
    feature     TEXT NOT NULL,
    value       TEXT NOT NULL,
    PRIMARY KEY (plan_id, feature)
)
`

// createTenantSubscriptionsTable's plan_id FK carries no ON DELETE
// behavior — matching the doc, which specifies none — since a plan
// referenced by a live subscription is never expected to be deleted;
// plans.is_active is the documented way to retire one instead.
const createTenantSubscriptionsTable = `
CREATE TABLE IF NOT EXISTS system.tenant_subscriptions (
    id                   UUID PRIMARY KEY DEFAULT uuidv7(),
    tenant_id            UUID NOT NULL REFERENCES system.tenants(id) ON DELETE CASCADE,
    plan_id              UUID NOT NULL REFERENCES system.plans(id),
    status               TEXT NOT NULL DEFAULT 'trialing'
                             CHECK (status IN (
                                 'trialing','active','past_due','cancelled','paused'
                             )),
    current_period_start TIMESTAMPTZ NOT NULL,
    current_period_end   TIMESTAMPTZ NOT NULL,
    trial_ends_at        TIMESTAMPTZ,
    cancelled_at         TIMESTAMPTZ,
    external_id          TEXT,
    created_at           TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at           TIMESTAMPTZ NOT NULL DEFAULT NOW()
)
`

const createTenantEntitlementOverridesTable = `
CREATE TABLE IF NOT EXISTS system.tenant_entitlement_overrides (
    tenant_id   UUID NOT NULL REFERENCES system.tenants(id) ON DELETE CASCADE,
    feature     TEXT NOT NULL,
    value       TEXT NOT NULL,
    reason      TEXT,
    expires_at  TIMESTAMPTZ,
    granted_by  UUID,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (tenant_id, feature)
)
`

// createTenantModuleSettingsTable matches multitenancy-internals.md §8's
// definition, except disabled_by carries no REFERENCES system.users(id)
// FK — same deviation tenant_entitlement_overrides.granted_by already
// makes above, and for the same reason: Bootstrap runs before
// user.Store.Bootstrap (engine.go), so system.users doesn't exist yet on
// a fresh database when this table is created.
const createTenantModuleSettingsTable = `
CREATE TABLE IF NOT EXISTS system.tenant_module_settings (
    tenant_id           UUID    NOT NULL REFERENCES system.tenants(id) ON DELETE CASCADE,
    module_name         TEXT    NOT NULL,
    enabled             BOOLEAN NOT NULL DEFAULT TRUE,
    disabled_at         TIMESTAMPTZ,
    disabled_by         UUID,
    provider_category   TEXT,
    PRIMARY KEY (tenant_id, module_name)
)
`

type Store struct {
	db *sql.DB
}

func NewStore(db *sql.DB) *Store {
	return &Store{db: db}
}

// Bootstrap creates system.plans, system.plan_entitlements,
// system.tenant_subscriptions, system.tenant_entitlement_overrides, and
// system.tenant_module_settings if they don't already exist, in FK-safe
// order. Idempotent and concurrent-safe against other processes calling
// Bootstrap at the same time, same convention tenant.Store.Bootstrap uses
// (goerp#171).
func (s *Store) Bootstrap(ctx context.Context) error {
	keys := []int64{db.SystemSchemaLockKey, db.AdvisoryLockKey("billing.Bootstrap")}
	return db.WithAdvisoryLock(ctx, s.db, keys, func(tx *sql.Tx) error {
		if err := db.EnsureSystemSchema(ctx, tx); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, createPlansTable); err != nil {
			return fmt.Errorf("create plans table: %w", err)
		}
		if _, err := tx.ExecContext(ctx, createPlanEntitlementsTable); err != nil {
			return fmt.Errorf("create plan_entitlements table: %w", err)
		}
		if _, err := tx.ExecContext(ctx, createTenantSubscriptionsTable); err != nil {
			return fmt.Errorf("create tenant_subscriptions table: %w", err)
		}
		if _, err := tx.ExecContext(ctx, createTenantEntitlementOverridesTable); err != nil {
			return fmt.Errorf("create tenant_entitlement_overrides table: %w", err)
		}
		if _, err := tx.ExecContext(ctx, createTenantModuleSettingsTable); err != nil {
			return fmt.Errorf("create tenant_module_settings table: %w", err)
		}
		return nil
	})
}

// CreatePlan inserts a new plan. priceMonthly/priceYearly may be nil
// (custom/enterprise pricing); currency and is_active take their SQL
// defaults ('USD', TRUE), same minimalism tenant.Store.CreateTenant uses
// for the columns it doesn't take as parameters.
func (s *Store) CreatePlan(ctx context.Context, name, displayName string, priceMonthly, priceYearly *int64) (*Plan, error) {
	row := s.db.QueryRowContext(ctx, `
		INSERT INTO system.plans (name, display_name, price_monthly, price_yearly)
		VALUES ($1, $2, $3, $4)
		RETURNING id, name, display_name, price_monthly, price_yearly, currency, is_active, created_at
	`, name, displayName, priceMonthly, priceYearly)

	var p Plan
	if err := row.Scan(&p.ID, &p.Name, &p.DisplayName, &p.PriceMonthly, &p.PriceYearly, &p.Currency, &p.IsActive, &p.CreatedAt); err != nil {
		return nil, fmt.Errorf("insert plan: %w", err)
	}
	return &p, nil
}

// UpsertPlanEntitlement inserts or replaces planID's feature grant — the
// table's PK is exactly (plan_id, feature), so a second call for the same
// pair updates value rather than erroring.
func (s *Store) UpsertPlanEntitlement(ctx context.Context, planID, feature, value string) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO system.plan_entitlements (plan_id, feature, value)
		VALUES ($1, $2, $3)
		ON CONFLICT (plan_id, feature) DO UPDATE SET value = EXCLUDED.value
	`, planID, feature, value)
	if err != nil {
		return fmt.Errorf("upsert plan entitlement: %w", err)
	}
	return nil
}

// CreateSubscription inserts a new tenant_subscriptions row; status
// defaults to 'trialing'.
func (s *Store) CreateSubscription(ctx context.Context, tenantID, planID string, currentPeriodStart, currentPeriodEnd time.Time) (*Subscription, error) {
	row := s.db.QueryRowContext(ctx, `
		INSERT INTO system.tenant_subscriptions (tenant_id, plan_id, current_period_start, current_period_end)
		VALUES ($1, $2, $3, $4)
		RETURNING id, tenant_id, plan_id, status, current_period_start, current_period_end, trial_ends_at, cancelled_at, external_id, created_at, updated_at
	`, tenantID, planID, currentPeriodStart, currentPeriodEnd)

	var sub Subscription
	if err := row.Scan(&sub.ID, &sub.TenantID, &sub.PlanID, &sub.Status, &sub.CurrentPeriodStart, &sub.CurrentPeriodEnd, &sub.TrialEndsAt, &sub.CancelledAt, &sub.ExternalID, &sub.CreatedAt, &sub.UpdatedAt); err != nil {
		return nil, fmt.Errorf("insert subscription: %w", err)
	}
	return &sub, nil
}

// UpsertEntitlementOverride inserts or replaces tenantID's override for
// feature — the table's PK is (tenant_id, feature).
func (s *Store) UpsertEntitlementOverride(ctx context.Context, tenantID, feature, value string, reason *string, expiresAt *time.Time, grantedBy *string) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO system.tenant_entitlement_overrides (tenant_id, feature, value, reason, expires_at, granted_by)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (tenant_id, feature) DO UPDATE SET
			value = EXCLUDED.value,
			reason = EXCLUDED.reason,
			expires_at = EXCLUDED.expires_at,
			granted_by = EXCLUDED.granted_by
	`, tenantID, feature, value, reason, expiresAt, grantedBy)
	if err != nil {
		return fmt.Errorf("upsert entitlement override: %w", err)
	}
	return nil
}

// PlanEntitlementsForTenant returns the entitlements granted by
// tenantID's current plan, via whichever of its subscriptions is
// currently trialing or active — the exact query
// multitenancy-internals.md §4's loadEntitlements pseudocode specifies.
// Building an EntitlementSet from the result is goerp#229's job.
func (s *Store) PlanEntitlementsForTenant(ctx context.Context, tenantID string) ([]PlanEntitlement, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT pe.plan_id, pe.feature, pe.value
		FROM system.plan_entitlements pe
		JOIN system.tenant_subscriptions ts ON ts.plan_id = pe.plan_id
		WHERE ts.tenant_id = $1 AND ts.status IN ('trialing', 'active')
	`, tenantID)
	if err != nil {
		return nil, fmt.Errorf("query plan entitlements for tenant: %w", err)
	}
	defer func() { _ = rows.Close() }()

	ents := []PlanEntitlement{}
	for rows.Next() {
		var e PlanEntitlement
		if err := rows.Scan(&e.PlanID, &e.Feature, &e.Value); err != nil {
			return nil, fmt.Errorf("scan plan entitlement: %w", err)
		}
		ents = append(ents, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate plan entitlements: %w", err)
	}
	return ents, nil
}

// ActiveOverridesForTenant returns tenantID's currently-active (unexpired)
// entitlement overrides — the exact query multitenancy-internals.md §4's
// loadEntitlements pseudocode specifies.
func (s *Store) ActiveOverridesForTenant(ctx context.Context, tenantID string) ([]EntitlementOverride, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT tenant_id, feature, value, reason, expires_at, granted_by, created_at
		FROM system.tenant_entitlement_overrides
		WHERE tenant_id = $1 AND (expires_at IS NULL OR expires_at > NOW())
	`, tenantID)
	if err != nil {
		return nil, fmt.Errorf("query active overrides for tenant: %w", err)
	}
	defer func() { _ = rows.Close() }()

	overrides := []EntitlementOverride{}
	for rows.Next() {
		var o EntitlementOverride
		if err := rows.Scan(&o.TenantID, &o.Feature, &o.Value, &o.Reason, &o.ExpiresAt, &o.GrantedBy, &o.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan entitlement override: %w", err)
		}
		overrides = append(overrides, o)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate active overrides: %w", err)
	}
	return overrides, nil
}

// SetModuleEnabledForTenant upserts tenantID's tenant_module_settings row
// for moduleName. disabledBy is nil when enabling (re-enabling a
// previously disabled module leaves disabled_at/disabled_by from the last
// disable in place — multitenancy-internals.md §8 doesn't specify
// clearing them, and they're only ever read while enabled is false).
func (s *Store) SetModuleEnabledForTenant(ctx context.Context, tenantID, moduleName string, enabled bool, disabledBy *string) error {
	var disabledAt *time.Time
	if !enabled {
		now := time.Now().UTC()
		disabledAt = &now
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO system.tenant_module_settings (tenant_id, module_name, enabled, disabled_at, disabled_by)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (tenant_id, module_name) DO UPDATE SET
			enabled = EXCLUDED.enabled,
			disabled_at = EXCLUDED.disabled_at,
			disabled_by = EXCLUDED.disabled_by
	`, tenantID, moduleName, enabled, disabledAt, disabledBy)
	if err != nil {
		return fmt.Errorf("upsert tenant module setting: %w", err)
	}
	return nil
}

// DisabledModulesForTenant returns the names of every module tenantID has
// explicitly disabled (enabled = false). A module absent from
// system.tenant_module_settings entirely is not disabled — the table only
// ever needs a row for a tenant's actual overrides, per
// multitenancy-internals.md §8.
func (s *Store) DisabledModulesForTenant(ctx context.Context, tenantID string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT module_name
		FROM system.tenant_module_settings
		WHERE tenant_id = $1 AND enabled = FALSE
	`, tenantID)
	if err != nil {
		return nil, fmt.Errorf("query disabled modules for tenant: %w", err)
	}
	defer func() { _ = rows.Close() }()

	names := []string{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, fmt.Errorf("scan disabled module name: %w", err)
		}
		names = append(names, name)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate disabled modules: %w", err)
	}
	return names, nil
}
