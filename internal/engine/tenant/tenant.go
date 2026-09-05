// Package tenant is the core tenant registry: system.tenants and
// system.tenant_domains, the tenant identity model everything else in
// multitenancy builds on (multitenancy-internals.md §1-2). It covers the
// data model only — billing/entitlements and schema-per-tenant
// provisioning are separate, larger concerns.
package tenant

import "time"

type Status string

const (
	StatusProvisioning Status = "provisioning"
	StatusActive       Status = "active"
	StatusSuspended    Status = "suspended"
	StatusOffboarding  Status = "offboarding"
	StatusDeleted      Status = "deleted"
)

type Plan string

const (
	PlanStarter    Plan = "starter"
	PlanPro        Plan = "pro"
	PlanEnterprise Plan = "enterprise"
	PlanInternal   Plan = "internal"
)

// AllPlans is every value system.tenants.plan's own CHECK constraint
// accepts — the single source of truth other packages (e.g.
// planchange's admin-facing plan-change endpoint) validate a requested
// plan name against, instead of duplicating this enum.
var AllPlans = []Plan{PlanStarter, PlanPro, PlanEnterprise, PlanInternal}

type Tenant struct {
	ID            string     `json:"id"`
	Slug          string     `json:"slug"`
	Name          string     `json:"name"`
	Plan          Plan       `json:"plan"`
	Status        Status     `json:"status"`
	Region        string     `json:"region"`
	Country       *string    `json:"country,omitempty"`
	TrialEndsAt   *time.Time `json:"trial_ends_at,omitempty"`
	CreatedAt     time.Time  `json:"created_at"`
	UpdatedAt     time.Time  `json:"updated_at"`
	ActivatedAt   *time.Time `json:"activated_at,omitempty"`
	SuspendedAt   *time.Time `json:"suspended_at,omitempty"`
	SuspendedBy   *string    `json:"suspended_by,omitempty"`
	SuspendReason *string    `json:"suspend_reason,omitempty"`
	DeletedAt     *time.Time `json:"deleted_at,omitempty"`
	// OffboardDeletionStartedAt is set the moment OffboardTenantWorkflow (or
	// the immediate-offboard River job) commits to actually deleting the
	// tenant's data — the single source of truth for "is this offboard still
	// cancellable." nil while the tenant is only marked StatusOffboarding
	// and still inside its grace period.
	OffboardDeletionStartedAt *time.Time `json:"offboard_deletion_started_at,omitempty"`
}

type DomainType string

const (
	DomainSubdomain DomainType = "subdomain"
	DomainCustom    DomainType = "custom"
)

type Domain struct {
	ID         string     `json:"id"`
	TenantID   string     `json:"tenant_id"`
	Domain     string     `json:"domain"`
	Type       DomainType `json:"type"`
	IsPrimary  bool       `json:"is_primary"`
	VerifiedAt *time.Time `json:"verified_at,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
}
