// Package retention sweeps expired module records and previews eligible deletion policies.
package retention

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"slices"
	"strings"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/dbscope"
	"github.com/djangbahevans/goerp/internal/engine/manifest"
	"github.com/djangbahevans/goerp/internal/engine/modeltable"
	"github.com/djangbahevans/goerp/internal/engine/module"
	"github.com/djangbahevans/goerp/internal/engine/registry"
	"github.com/djangbahevans/goerp/internal/engine/tenant"
	tenantresolve "github.com/djangbahevans/goerp/internal/engine/tenant/resolve"
	"github.com/jackc/pgx/v5"
	"github.com/rs/zerolog/log"
)

type TenantLister interface {
	ActiveTenants(context.Context) ([]tenant.Tenant, error)
}

type EntitlementLoader interface {
	LoadCurrentEntitlements(context.Context, string) (tenantresolve.EntitlementSet, error)
}

type Sweeper struct {
	// Pool uses the table-owning schema-sync role so retention covers rows hidden by user RLS.
	Pool         *sql.DB
	Registry     *registry.ModuleRegistry
	Tenants      TenantLister
	Entitlements EntitlementLoader
}

type Result struct {
	At       time.Time      `json:"at"`
	Preview  bool           `json:"preview"`
	Policies []PolicyResult `json:"policies"`
}

type PolicyResult struct {
	TenantID   string    `json:"tenant_id"`
	TenantSlug string    `json:"tenant_slug"`
	Module     string    `json:"module"`
	Policy     int       `json:"policy"`
	Table      string    `json:"table"`
	Action     string    `json:"action"`
	Status     string    `json:"status"`
	Affected   int64     `json:"affected"`
	Cutoff     time.Time `json:"cutoff,omitzero"`
	Diagnostic string    `json:"diagnostic,omitempty"`
}

// Run uses one cutoff instant for every policy. Skipped policies have diagnostics;
// database failures are returned after the remaining tenants and policies run.
func (s *Sweeper) Run(ctx context.Context, at time.Time, preview bool) (Result, error) {
	result := Result{At: at.UTC(), Preview: preview, Policies: []PolicyResult{}}
	if at.IsZero() {
		return result, errors.New("retention sweep time is required")
	}

	snap := s.Registry.Snapshot()
	if snap == nil {
		return result, errors.New("module registry has no snapshot yet")
	}

	modules := snap.Modules()
	var names []string
	for name, mod := range modules {
		if mod.Status == module.StatusReady && len(mod.Manifest.RetentionPolicies) > 0 {
			names = append(names, name)
		}
	}
	slices.Sort(names)
	if len(names) == 0 {
		return result, nil
	}

	tenants, err := s.Tenants.ActiveTenants(ctx)
	if err != nil {
		return result, fmt.Errorf("list active tenants: %w", err)
	}

	var failures []error
	for _, tn := range tenants {
		ents, err := s.Entitlements.LoadCurrentEntitlements(ctx, tn.ID)
		if err != nil {
			failures = append(failures, fmt.Errorf("tenant %s: load entitlements: %w", tn.Slug, err))
			continue
		}

		for _, name := range names {
			if !ents.ModuleEnabled(name) {
				continue
			}

			for index, policy := range modules[name].Manifest.RetentionPolicies {
				p := PolicyResult{
					TenantID:   tn.ID,
					TenantSlug: tn.Slug,
					Module:     name,
					Policy:     index,
					Table:      policy.Table,
					Action:     policy.Action,
				}

				if diagnostic := policyDiagnostic(modules, name, policy, preview); diagnostic != "" {
					p.Status = "skipped"
					p.Diagnostic = diagnostic
					log.Warn().Str("tenant", tn.Slug).Str("module", name).Int("policy", index).
						Str("table", policy.Table).Str("diagnostic", diagnostic).Msg("retention policy skipped")
				} else {
					p.Cutoff = result.At.Add(-time.Duration(policy.RetainDays) * 24 * time.Hour)
					p.Affected, err = s.execute(ctx, tn.Slug, policy.Table, p.Cutoff, preview)
					if err != nil {
						p.Status = "failed"
						p.Diagnostic = err.Error()
						failures = append(failures, fmt.Errorf("tenant %s module %s retention policy %d: %w", tn.Slug, name, index, err))
					} else if preview {
						p.Status = "previewed"
					} else {
						p.Status = "deleted"
					}
				}

				result.Policies = append(result.Policies, p)
			}
		}
	}

	return result, errors.Join(failures...)
}

func policyDiagnostic(modules map[string]*module.LoadedModule, owner string, p manifest.RetentionPolicy, preview bool) string {
	if p.Condition != "" {
		return "condition evaluation is unsupported"
	}
	if p.Action != "delete" {
		return "unsupported action: " + p.Action
	}
	if p.RetainDays <= 0 || int64(p.RetainDays) > math.MaxInt64/int64(24*time.Hour) {
		return "retain_days must be positive and fit a duration of 24-hour days"
	}
	if p.Table == "" || len(p.Table) > 63 || strings.ContainsRune(p.Table, 0) || dbscope.IsReservedTableName(p.Table) {
		return "invalid or reserved table name"
	}

	owned := false
	for name, mod := range modules {
		for _, md := range mod.ModelDecls {
			if md.Backend != "" || modeltable.Name(md) != p.Table {
				continue
			}
			if name != owner {
				return "table is declared by another module"
			}
			owned = true
		}
	}
	if !owned {
		return "table is not a physical table owned by the declaring module"
	}
	if preview && !p.IsDryRunSafe() {
		return "policy disallows preview"
	}

	return ""
}

func (s *Sweeper) execute(ctx context.Context, slug, table string, cutoff time.Time, preview bool) (int64, error) {
	qualified := pgx.Identifier{"tenant_" + slug, table}.Sanitize()
	if preview {
		var count int64
		if err := s.Pool.QueryRowContext(ctx, "SELECT count(*) FROM "+qualified+" WHERE created_at < $1", cutoff).Scan(&count); err != nil {
			return 0, fmt.Errorf("count expired records: %w", err)
		}

		return count, nil
	}

	result, err := s.Pool.ExecContext(ctx, "DELETE FROM "+qualified+" WHERE created_at < $1", cutoff)
	if err != nil {
		return 0, fmt.Errorf("delete expired records: %w", err)
	}

	count, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("read deleted record count: %w", err)
	}

	return count, nil
}
