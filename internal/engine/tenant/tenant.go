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

type Tenant struct {
	ID            string
	Slug          string
	Name          string
	Plan          Plan
	Status        Status
	Region        string
	TrialEndsAt   *time.Time
	CreatedAt     time.Time
	UpdatedAt     time.Time
	SuspendedAt   *time.Time
	SuspendedBy   *string
	SuspendReason *string
	DeletedAt     *time.Time
}

type DomainType string

const (
	DomainSubdomain DomainType = "subdomain"
	DomainCustom    DomainType = "custom"
)

type Domain struct {
	ID         string
	TenantID   string
	Domain     string
	Type       DomainType
	IsPrimary  bool
	VerifiedAt *time.Time
	CreatedAt  time.Time
}
