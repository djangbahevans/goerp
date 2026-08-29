package tenantsync

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/jobqueue"
	"github.com/djangbahevans/goerp/internal/engine/module"
	"github.com/djangbahevans/goerp/internal/engine/registry"
	"github.com/djangbahevans/goerp/internal/engine/schema"
	"github.com/djangbahevans/goerp/internal/engine/tenant"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"
	"github.com/rs/zerolog/log"
)

// Admin satisfies adminapi's SchemaStatusReader/SchemaDiffer/SchemaSyncer/
// SchemaAccepter — goerp#292's admin API schema route surface's entry
// point into this package and internal/engine/schema, the same "small
// struct wrapping the real stores + a job client" shape
// tenantexport.Exporter/tenantimport.Importer already use for their own
// admin API seams.
type Admin struct {
	tenantStore *tenant.Store
	registry    *registry.ModuleRegistry
	pool        *schema.SchemaSyncPool
	diffEngine  *schema.SchemaDiffEngine
	jobClient   *river.Client[pgx.Tx]
	jobQueue    string
}

func NewAdmin(tenantStore *tenant.Store, reg *registry.ModuleRegistry, pool *schema.SchemaSyncPool, diffEngine *schema.SchemaDiffEngine, jobClient *river.Client[pgx.Tx], jobQueue string) *Admin {
	return &Admin{tenantStore: tenantStore, registry: reg, pool: pool, diffEngine: diffEngine, jobClient: jobClient, jobQueue: jobQueue}
}

// Status returns GET /admin/schema/status's rows. filter is one of
// "ok"/"failed"/"in_progress" (matched directly against
// schema_sync_status), "pending" (module_schema_versions has no stored
// status for this — narrowed here by running a live Diff against every
// candidate row and keeping only the ones with at least one currently-
// blocked change), or "" (no status filter at all).
func (a *Admin) Status(ctx context.Context, tenantSlug, moduleName, filter string) ([]schema.TenantModuleStatus, error) {
	if filter != "pending" {
		return a.pool.StatusFiltered(ctx, tenantSlug, moduleName, filter)
	}

	candidates, err := a.pool.StatusFiltered(ctx, tenantSlug, moduleName, "")
	if err != nil {
		return nil, err
	}

	snap := a.registry.Snapshot()
	if snap == nil {
		return nil, fmt.Errorf("module registry not ready")
	}

	var mu sync.Mutex
	var pending []schema.TenantModuleStatus

	// fanOut gives this the same bounded-concurrency shape syncModule
	// (tenantsync.go) already uses for the identical "one per-(tenant,
	// module) unit of work, fan out across candidates" shape — a status
	// sweep across many tenants/modules would otherwise run every live
	// Diff strictly one at a time. A single pair's failure is logged and
	// skipped, never aborting the rest of the sweep — this package's own
	// doc comment states that principle (tenantsync.go: "never letting
	// one tenant's failure block another's"), and syncModule already
	// honors it for the identical per-pair shape.
	fanOut(candidates, DefaultConcurrency, func(s schema.TenantModuleStatus) {
		mod, ok := snap.Modules()[s.ModuleName]
		if !ok {
			return
		}

		// s.TenantID comes straight from StatusFiltered's own join
		// against system.tenants — no second tenantStore.GetBySlug
		// round trip needed per candidate.
		t := tenant.Tenant{ID: s.TenantID, Slug: s.TenantSlug}
		_, _, blocked, err := a.diffModule(ctx, t, mod)
		if err != nil {
			log.Error().Err(err).Str("tenant", s.TenantSlug).Str("module", s.ModuleName).Msg("schema status pending check: diff failed")
			return
		}
		if len(blocked) > 0 {
			mu.Lock()
			pending = append(pending, s)
			mu.Unlock()
		}
	})

	// Concurrent completion order is arbitrary — resort to match
	// StatusFiltered's own ORDER BY t.slug, v.module_name so a "pending"
	// listing doesn't reshuffle between otherwise-identical calls.
	sort.Slice(pending, func(i, j int) bool {
		if pending[i].TenantSlug != pending[j].TenantSlug {
			return pending[i].TenantSlug < pending[j].TenantSlug
		}
		return pending[i].ModuleName < pending[j].ModuleName
	})
	return pending, nil
}

// Diff computes tenantSlug/moduleName's pending safe/deferred/blocked
// change set without applying any of it — GET /admin/modules/{name}/schema,
// a synchronous, side-effect-free read. Returns the module's current
// version alongside the three buckets rather than a wrapper struct, so
// adminapi's own SchemaDiffer interface can stay independent of any type
// this package declares (adminapi builds its own response shape from
// these pieces, the same way it already does for schema.ChangeSummary).
// verbose controls whether each ChangeSummary keeps its Detail field —
// cli-reference.md §4: "--verbose: Include full column definitions (not
// just change summary)" — so the non-verbose default strips Detail down
// to just Kind/Table/Hash, and verbose keeps the fuller column/type
// description describeChange already builds.
func (a *Admin) Diff(ctx context.Context, tenantSlug, moduleName string, verbose bool) (version string, safe, deferred, blocked []schema.ChangeSummary, err error) {
	t, mod, err := resolveTenantModule(ctx, a.tenantStore, a.registry, tenantSlug, moduleName)
	if err != nil {
		return "", nil, nil, nil, err
	}

	safe, deferred, blocked, err = a.diffModule(ctx, t, mod)
	if err != nil {
		return "", nil, nil, nil, err
	}

	if !verbose {
		safe, deferred, blocked = stripDetail(safe), stripDetail(deferred), stripDetail(blocked)
	}

	return mod.Manifest.Version, safe, deferred, blocked, nil
}

func stripDetail(summaries []schema.ChangeSummary) []schema.ChangeSummary {
	out := make([]schema.ChangeSummary, len(summaries))
	for i, s := range summaries {
		s.Detail = ""
		out[i] = s
	}
	return out
}

func (a *Admin) diffModule(ctx context.Context, t tenant.Tenant, mod *module.LoadedModule) (safe, deferred, blocked []schema.ChangeSummary, err error) {
	// BeginRead, not BeginSync: this is a read-only Diff, and taking the
	// same pg_advisory_lock a real sync holds for its whole DDL duration
	// would make this "synchronous, side-effect-free read" block on (and
	// potentially time out against) unrelated write traffic for no
	// correctness benefit — see BeginRead's own doc comment.
	sess, err := a.pool.BeginRead(ctx, t.ID, t.Slug, mod.Manifest.Name, &mod.Manifest)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("begin read session: %w", err)
	}
	defer func() { _ = sess.Close(ctx) }()

	changes, err := a.diffEngine.Diff(ctx, sess, mod.ModelDecls, mod.TypeDecls)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("diff schema: %w", err)
	}

	safe, deferred, blocked = a.diffEngine.Classify(changes)
	return safe, deferred, blocked, nil
}

// StartSync enqueues a SyncArgs job — POST /admin/schema/sync. scheduledAt,
// when non-nil, becomes the job's ScheduledAt (River's own native deferred-
// job mechanism — cli-reference.md §4's `--schedule` is explicitly "not a
// new scheduler").
func (a *Admin) StartSync(ctx context.Context, tenantSlug, moduleName string, scheduledAt *time.Time) (jobID string, err error) {
	opts := &river.InsertOpts{Queue: a.jobQueue}
	if scheduledAt != nil {
		opts.ScheduledAt = *scheduledAt
	}

	insertResult, err := a.jobClient.Insert(ctx, SyncArgs{TenantSlug: tenantSlug, ModuleName: moduleName}, opts)
	if err != nil {
		return "", fmt.Errorf("enqueue schema sync job: %w", err)
	}
	return jobqueue.EncodeJobID(insertResult.Job.ID), nil
}

// Accept re-diffs tenantSlug/moduleName live, records one
// system.schema_sync_acceptances row per currently-blocked change (the
// request itself carries no diff hash — cli-reference.md §4's
// {module, tenant, reason} body — so "accept" means "authorize everything
// blocked right now"), then enqueues the one-time resync job. Returns
// ErrNothingBlocked if there's nothing to accept, so the handler can
// report that as a usage error rather than silently writing zero rows and
// still returning a job id.
func (a *Admin) Accept(ctx context.Context, tenantSlug, moduleName, reason, operator string) (acceptanceIDs []string, jobID string, err error) {
	t, mod, err := resolveTenantModule(ctx, a.tenantStore, a.registry, tenantSlug, moduleName)
	if err != nil {
		return nil, "", err
	}

	_, _, blocked, err := a.diffModule(ctx, t, mod)
	if err != nil {
		return nil, "", err
	}
	if len(blocked) == 0 {
		return nil, "", ErrNothingBlocked
	}

	// Skip a hash that already has an unconsumed acceptance under this
	// exact module version — a retry after, say, the job-enqueue failure
	// below shouldn't pile up a fresh audit row for the same still-
	// blocked change every time. The partial unique index backing
	// RecordAcceptance is the actual race-proof guard (this check is
	// just the fast, no-conflict-error common path).
	alreadyAccepted, err := a.pool.AcceptedHashes(ctx, t.ID, moduleName, mod.Manifest.Version)
	if err != nil {
		return nil, "", err
	}

	for _, b := range blocked {
		if alreadyAccepted[b.Hash] {
			continue
		}
		id, err := a.pool.RecordAcceptance(ctx, t.ID, moduleName, mod.Manifest.Version, b.Hash, reason, operator)
		if err != nil {
			return acceptanceIDs, "", err
		}
		acceptanceIDs = append(acceptanceIDs, id)
	}

	insertResult, err := a.jobClient.Insert(ctx, AcceptResyncArgs{TenantSlug: tenantSlug, ModuleName: moduleName}, &river.InsertOpts{Queue: a.jobQueue})
	if err != nil {
		// acceptanceIDs already committed to system.schema_sync_acceptances
		// at this point (RecordAcceptance above) — the error message names
		// them explicitly, since the caller (adminapi's accept handler)
		// only ever surfaces this error's text, not acceptanceIDs itself,
		// to whoever is retrying and needs to know these rows already
		// exist rather than assume nothing was recorded.
		return acceptanceIDs, "", fmt.Errorf("recorded acceptance(s) %v but failed to enqueue resync job: %w", acceptanceIDs, err)
	}
	return acceptanceIDs, jobqueue.EncodeJobID(insertResult.Job.ID), nil
}

// ErrNothingBlocked is returned by Accept when tenantSlug/moduleName has
// no currently-blocked change to authorize.
var ErrNothingBlocked = errors.New("no blocked schema change to accept")

// ErrModuleNotLoaded is returned by resolve when moduleName isn't
// currently loaded — adminapi's schema handlers map this (like
// tenant.ErrTenantNotFound, wrapped straight through from
// a.tenantStore.GetBySlug below) to a 404 rather than a generic 500,
// matching activitydispatch.go's own "module_not_found" convention for
// the identical case.
var ErrModuleNotLoaded = errors.New("module not loaded")

// resolveModule finds name in snap's loaded modules, wrapping a miss as
// ErrModuleNotLoaded — the shared "look up one module, sentinel on miss"
// primitive resolveTenantModule and SyncWorker.resolveModules both build
// on.
func resolveModule(snap *registry.RegistrySnapshot, name string) (*module.LoadedModule, error) {
	mod, ok := snap.Modules()[name]
	if !ok {
		return nil, fmt.Errorf("module %q: %w", name, ErrModuleNotLoaded)
	}
	return mod, nil
}

// resolveTenantModule looks up a tenant by slug and a module by name in
// one call — the exact (tenant, module) pair Admin's Diff/Accept and
// AcceptResyncWorker.Work each need before doing anything else.
func resolveTenantModule(ctx context.Context, tenantStore *tenant.Store, reg *registry.ModuleRegistry, tenantSlug, moduleName string) (tenant.Tenant, *module.LoadedModule, error) {
	t, err := tenantStore.GetBySlug(ctx, tenantSlug)
	if err != nil {
		return tenant.Tenant{}, nil, fmt.Errorf("look up tenant %q: %w", tenantSlug, err)
	}

	snap := reg.Snapshot()
	if snap == nil {
		return tenant.Tenant{}, nil, fmt.Errorf("module registry not ready")
	}
	mod, err := resolveModule(snap, moduleName)
	if err != nil {
		return tenant.Tenant{}, nil, err
	}

	return *t, mod, nil
}
