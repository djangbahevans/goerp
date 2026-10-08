// Package billing manages plans, subscriptions, entitlements and tenant overrides.
// Entitlement loading interprets these stored values and caches the resulting grants.
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

// Plan represents a system.plans row. Prices use minor units; nil indicates custom
// pricing.
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

// PlanEntitlement grants a text value such as "true", "50" or "unlimited". Feature keys
// use module.{name} for module access and {resource}.{limit_name} for limits.
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
