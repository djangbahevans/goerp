// Package membership finds the tenants a platform-level account belongs
// to, for a sign-in or verification resend that names no tenant
// (auth-internals.md §3 "Cross-tenant user membership"). Membership lives
// only in each tenant's own user_roles, so the lookup scans every active
// tenant in one query rather than reading a system-level copy.
package membership

import (
	"context"
	"fmt"

	"github.com/djangbahevans/goerp/internal/engine/role"
	"github.com/djangbahevans/goerp/internal/engine/tenant"
)

// TenantsOf returns the active tenants where userID holds a live role
// grant, ordered by slug.
func TenantsOf(ctx context.Context, tenants *tenant.Store, roles *role.Store, userID string) ([]tenant.Tenant, error) {
	active, err := tenants.ActiveTenants(ctx)
	if err != nil {
		return nil, fmt.Errorf("list active tenants: %w", err)
	}
	slugs := make([]string, len(active))
	bySlug := make(map[string]tenant.Tenant, len(active))
	for i, t := range active {
		slugs[i] = t.Slug
		bySlug[t.Slug] = t
	}
	memberSlugs, err := roles.MemberOf(ctx, slugs, userID)
	if err != nil {
		return nil, err
	}
	out := make([]tenant.Tenant, len(memberSlugs))
	for i, slug := range memberSlugs {
		out[i] = bySlug[slug]
	}
	return out, nil
}
