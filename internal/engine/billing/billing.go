// Package billing owns the plans/entitlements/subscriptions data model —
// multitenancy-internals.md §2's "Plans and their entitlements" tables —
// the schema goerp#229's future entitlement-loading step (§4
// loadEntitlements) needs to query. It covers the data model and the
// minimal insert/query primitives to exercise it; building the
// EntitlementSet result type and the Redis caching layer around
// loadEntitlements itself is goerp#229's job, not this package's.
package billing

import "time"

type SubscriptionStatus string

const (
	SubscriptionTrialing  SubscriptionStatus = "trialing"
	SubscriptionActive    SubscriptionStatus = "active"
	SubscriptionPastDue   SubscriptionStatus = "past_due"
	SubscriptionCancelled SubscriptionStatus = "cancelled"
	SubscriptionPaused    SubscriptionStatus = "paused"
)

// Plan is one system.plans row. PriceMonthly/PriceYearly are minor units
// (e.g. cents); nil means custom/enterprise pricing, per the doc's own
// column comment.
type Plan struct {
	ID           string    `json:"id"`
	Name         string    `json:"name"`
	DisplayName  string    `json:"display_name"`
	PriceMonthly *int64    `json:"price_monthly,omitempty"`
	PriceYearly  *int64    `json:"price_yearly,omitempty"`
	Currency     string    `json:"currency"`
	IsActive     bool      `json:"is_active"`
	CreatedAt    time.Time `json:"created_at"`
}

// PlanEntitlement is one system.plan_entitlements row — a single
// feature/value grant belonging to a plan. Feature keys follow
// multitenancy-internals.md §4's convention: "module.{module_name}" for
// module access, "{resource}.{limit_name}" for numeric limits. Value is
// always stored as text ("true", "50", "unlimited") — interpreting it
// against a specific feature's expected type is goerp#229's job when it
// builds an EntitlementSet from these rows.
type PlanEntitlement struct {
	PlanID  string `json:"plan_id"`
	Feature string `json:"feature"`
	Value   string `json:"value"`
}

// Subscription is one system.tenant_subscriptions row — a tenant's
// billing relationship to a plan over one period.
type Subscription struct {
	ID                 string             `json:"id"`
	TenantID           string             `json:"tenant_id"`
	PlanID             string             `json:"plan_id"`
	Status             SubscriptionStatus `json:"status"`
	CurrentPeriodStart time.Time          `json:"current_period_start"`
	CurrentPeriodEnd   time.Time          `json:"current_period_end"`
	TrialEndsAt        *time.Time         `json:"trial_ends_at,omitempty"`
	CancelledAt        *time.Time         `json:"cancelled_at,omitempty"`
	ExternalID         *string            `json:"external_id,omitempty"`
	CreatedAt          time.Time          `json:"created_at"`
	UpdatedAt          time.Time          `json:"updated_at"`
}

// EntitlementOverride is one system.tenant_entitlement_overrides row — a
// per-tenant enterprise-deal grant that stands independently of whatever
// the tenant's plan itself entitles.
type EntitlementOverride struct {
	TenantID  string     `json:"tenant_id"`
	Feature   string     `json:"feature"`
	Value     string     `json:"value"`
	Reason    *string    `json:"reason,omitempty"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
	GrantedBy *string    `json:"granted_by,omitempty"`
	CreatedAt time.Time  `json:"created_at"`
}
