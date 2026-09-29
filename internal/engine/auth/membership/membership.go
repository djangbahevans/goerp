// Package membership finds the tenants a platform-level account belongs
// to, for a sign-in, password-reset request or verification resend that
// names no tenant (auth-internals.md §3 "Cross-tenant user membership").
// Candidates come from the system.tenant_memberships index, and each is
// confirmed with the per-tenant membership check, since the index knows
// nothing of suspension or role expiry.
package membership

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/djangbahevans/goerp/internal/engine/role"
	"github.com/djangbahevans/goerp/internal/engine/tenant"
)

// TenantsOf returns the active tenants where userID is a member, ordered
// by slug.
func TenantsOf(ctx context.Context, tenants *tenant.Store, roles *role.Store, userID string) ([]tenant.Tenant, error) {
	ids, err := roles.CandidateTenantIDs(ctx, userID)
	if err != nil {
		return nil, err
	}
	var out []tenant.Tenant
	for _, id := range ids {
		t, err := tenants.GetByID(ctx, id)
		if err != nil {
			return nil, fmt.Errorf("load candidate tenant: %w", err)
		}
		if t.Status != tenant.StatusActive {
			continue
		}
		ok, err := roles.IsMember(ctx, t.Slug, userID)
		if err != nil {
			return nil, err
		}
		if ok {
			out = append(out, *t)
		}
	}
	slices.SortFunc(out, func(a, b tenant.Tenant) int { return strings.Compare(a.Slug, b.Slug) })
	return out, nil
}
