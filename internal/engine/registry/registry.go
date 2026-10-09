package registry

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json/v2"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"

	abiv1 "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/djangbahevans/goerp/internal/engine/computed"
	"github.com/djangbahevans/goerp/internal/engine/dataaudit"
	"github.com/djangbahevans/goerp/internal/engine/enginenotif"
	"github.com/djangbahevans/goerp/internal/engine/event"
	"github.com/djangbahevans/goerp/internal/engine/fieldsec"
	"github.com/djangbahevans/goerp/internal/engine/job"
	"github.com/djangbahevans/goerp/internal/engine/manifest"
	"github.com/djangbahevans/goerp/internal/engine/modelextension"
	"github.com/djangbahevans/goerp/internal/engine/modeltable"
	"github.com/djangbahevans/goerp/internal/engine/module"
	"github.com/djangbahevans/goerp/internal/engine/permission"
	"github.com/djangbahevans/goerp/internal/engine/policy"
	"github.com/djangbahevans/goerp/internal/engine/route"
	"github.com/djangbahevans/goerp/internal/engine/searchindex"
	"github.com/djangbahevans/goerp/sdk/go/model"
	"github.com/rs/zerolog/log"
)

// ErrReserved is Reserve's rejection when another writer already holds
// name — install, hot reload, or any other writer, whichever got there
// first.
var ErrReserved = errors.New("module name is reserved by another writer")

type ModuleRegistry struct {
	current atomic.Pointer[RegistrySnapshot]
	writeMu sync.Mutex

	reserveMu sync.Mutex
	reserved  map[string]struct{}
}

func (r *ModuleRegistry) Snapshot() *RegistrySnapshot {
	return r.current.Load()
}

// ModelDeclarations returns the model declarations of the named module in
// the current snapshot, or false when it is not loaded or has failed.
func (r *ModuleRegistry) ModelDeclarations(moduleName string) ([]model.ModelDeclaration, bool) {
	snap := r.Snapshot()
	if snap == nil {
		return nil, false
	}
	return snap.ModelDeclarations(moduleName)
}

// Update atomically replaces the module map. Concurrent read/merge writers must use
// UpdateWith to avoid losing additions from stale snapshots.
func (r *ModuleRegistry) Update(modules map[string]*module.LoadedModule) (*RegistrySnapshot, error) {
	return r.UpdateWith(func(map[string]*module.LoadedModule) (map[string]*module.LoadedModule, error) {
		return modules, nil
	})
}

// UpdateWith locks mutation and publication. Callers needing an atomic derived-cache
// update must hold Lock across UpdateWithLocked and that extra work.
func (r *ModuleRegistry) UpdateWith(mutate func(current map[string]*module.LoadedModule) (map[string]*module.LoadedModule, error)) (*RegistrySnapshot, error) {
	r.writeMu.Lock()
	defer r.writeMu.Unlock()
	return r.UpdateWithLocked(mutate)
}

// Lock and Unlock allow callers to keep the publication lock across UpdateWithLocked and
// related state changes.
func (r *ModuleRegistry) Lock()   { r.writeMu.Lock() }
func (r *ModuleRegistry) Unlock() { r.writeMu.Unlock() }

// UpdateWithLocked requires the registry write lock. It merges against the current map and
// aborts without publishing if mutate fails, preventing lost updates across different
// writer kinds.
func (r *ModuleRegistry) UpdateWithLocked(mutate func(current map[string]*module.LoadedModule) (map[string]*module.LoadedModule, error)) (*RegistrySnapshot, error) {
	old := r.current.Load() // may be nil on the very first call
	var currentModules map[string]*module.LoadedModule
	if old != nil {
		currentModules = old.modules
	}

	modules, err := mutate(currentModules)
	if err != nil {
		return nil, err
	}

	validateSyncSubscriptionCycles(modules)
	resolved, err := modelextension.Resolve(modules)
	if err != nil {
		return nil, err
	}

	routeTable, err := buildRouteTable(resolved)
	if err != nil {
		return nil, fmt.Errorf("build route table: %w", err)
	}

	jobRegistry, err := buildJobRegistry(modules)
	if err != nil {
		return nil, fmt.Errorf("build job registry: %w", err)
	}

	schemaHash := computeSchemaHash(resolved, routeTable)
	newSnap := &RegistrySnapshot{
		modules:          modules,
		resolved:         resolved,
		schemaHash:       schemaHash,
		schemaResponse:   buildSchemaResponse(resolved, routeTable, schemaHash),
		routeTable:       routeTable,
		eventRegistry:    buildEventRegistry(modules),
		permRegistry:     buildPermissionRegistry(modules),
		policyRegistry:   buildPolicyRegistry(resolved),
		fieldSecRegistry: buildFieldSecRegistry(resolved),
		searchIndexReg:   buildSearchIndexRegistry(modules),
		jobRegistry:      jobRegistry,
		cronRegistry:     buildCronRegistry(modules),
		computedIndex:    buildComputedIndex(resolved),
		dataAuditReg:     buildDataAuditRegistry(resolved),
		modelsByTable:    buildModelsByTable(modules),
	}
	if old != nil {
		newSnap.schemaRegistry = old.schemaRegistry
	}

	r.current.Store(newSnap)
	return newSnap, nil
}

// Reserve prevents install and reload from duplicating compile/sync work for one module.
// It is advisory; publication must recheck under the registry lock.
func (r *ModuleRegistry) Reserve(name string) (release func(), err error) {
	r.reserveMu.Lock()
	defer r.reserveMu.Unlock()

	if _, ok := r.reserved[name]; ok {
		return nil, fmt.Errorf("%w: %q", ErrReserved, name)
	}
	if r.reserved == nil {
		r.reserved = make(map[string]struct{})
	}
	r.reserved[name] = struct{}{}

	return func() {
		r.reserveMu.Lock()
		delete(r.reserved, name)
		r.reserveMu.Unlock()
	}, nil
}

// Failed modules must not claim routes, events, permissions, or field security. Skipping
// them also preserves load-time conflict isolation.

func buildFieldSecRegistry(modules map[string]*module.LoadedModule) *fieldsec.FieldSecurityRegistry {
	reg := fieldsec.New()
	for name, m := range modules {
		if m.Status == module.StatusFailed {
			continue
		}
		reg.Register(name, m.ModelDecls)
	}
	return reg
}

func buildPolicyRegistry(modules map[string]*module.LoadedModule) *policy.Registry {
	modelsByModule := make(map[string][]model.ModelDeclaration, len(modules))
	for name, m := range modules {
		if m.Status != module.StatusFailed {
			modelsByModule[name] = m.ModelDecls
		}
	}

	reg := policy.New()
	for _, m := range modules {
		if m.Status == module.StatusFailed {
			continue
		}
		reg.Register(m.Manifest.Policies, modelsByModule)
	}
	return reg
}

func buildSearchIndexRegistry(modules map[string]*module.LoadedModule) *searchindex.Registry {
	reg := searchindex.New()
	for name, m := range modules {
		if m.Status == module.StatusFailed {
			continue
		}
		reg.Register(name, m.Manifest.SearchIndexes)
	}
	return reg
}

func buildComputedIndex(modules map[string]*module.LoadedModule) *computed.Index {
	idx := computed.New()
	for name, m := range modules {
		if m.Status == module.StatusFailed {
			continue
		}
		idx.Register(name, m.ModelDecls)
	}
	return idx
}

func buildDataAuditRegistry(modules map[string]*module.LoadedModule) *dataaudit.Registry {
	reg := dataaudit.New()
	for name, m := range modules {
		if m.Status == module.StatusFailed {
			continue
		}
		reg.Register(name, m.Manifest.AuditedTables, m.ModelDecls)
	}
	return reg
}

// buildModelsByTable maps each non-failed module's model tables to their
// qualified model names.
func buildModelsByTable(modules map[string]*module.LoadedModule) map[string]string {
	out := make(map[string]string)
	for name, m := range modules {
		if m.Status == module.StatusFailed {
			continue
		}
		for _, decl := range m.ModelDecls {
			out[modeltable.Name(decl)] = decl.QualifiedName(name)
		}
	}
	return out
}

// These hash-only shapes select the API declarations that affect schema staleness. Fields
// added to the schema response must also enter the hash when they change the API surface.
type schemaHashRoute struct {
	Method         string
	Path           string
	Permissions    []string
	Model          string
	CrudAction     string
	Name           string
	ResponseIsList bool
	RequestType    *abiv1.TypeDesc
	ResponseType   *abiv1.TypeDesc
}

type schemaHashModel struct {
	Name        string
	Label       string
	LabelPlural string
	Fields      []schemaHashField
	EnabledOps  []string
	Shareable   bool
}

type schemaHashField struct {
	Name         string
	Kind         model.FieldKind
	Required     bool
	RelatedModel string
}

type schemaHashModule struct {
	Version           string
	Routes            []schemaHashRoute
	Views             []manifest.View
	Navigation        []manifest.NavGroup
	Models            map[string]schemaHashModel
	Permissions       []manifest.Permission
	PublicConfig      map[string]any
	NotificationTypes []SchemaNotificationType
}

// computeSchemaHash hashes a deterministic snapshot of the schema's declarations for cache
// invalidation.
func computeSchemaHash(modules map[string]*module.LoadedModule, routeTable *route.RouteTable) string {
	prefixes := make(map[string]string, len(modules))
	hashModules := make(map[string]schemaHashModule, len(modules))
	for name, m := range modules {
		if m.Status == module.StatusFailed {
			continue
		}
		prefixes[name] = route.ModulePathPrefix(name, m.Manifest.Type)

		models := make(map[string]schemaHashModel, len(m.ModelDecls))
		for _, md := range m.ModelDecls {
			fields := make([]schemaHashField, 0, len(md.Fields))
			for _, f := range md.Fields {
				fields = append(fields, schemaHashField{
					Name:         f.Name,
					Kind:         f.Def.Kind,
					Required:     f.Def.IsRequired,
					RelatedModel: f.Def.RelatedModel,
				})
			}
			ops := make([]string, 0, len(md.EnabledOps))
			for _, op := range md.EnabledOps {
				ops = append(ops, op.Name)
			}
			models[md.QualifiedName(name)] = schemaHashModel{
				Name:        md.ResourceName(),
				Label:       md.Label,
				LabelPlural: md.LabelPlural,
				Fields:      fields,
				EnabledOps:  ops,
				Shareable:   md.Shareable,
			}
		}

		publicConfig := map[string]any{}
		for _, c := range m.Manifest.ConfigSchema {
			if c.Public {
				publicConfig[c.Key] = c.Default
			}
		}

		hashModules[name] = schemaHashModule{
			Version:           m.Manifest.Version,
			Routes:            []schemaHashRoute{},
			Views:             m.Manifest.Views,
			Navigation:        m.Manifest.Navigation,
			Models:            models,
			Permissions:       m.Manifest.Permissions,
			PublicConfig:      publicConfig,
			NotificationTypes: schemaNotificationTypesFrom(m.Manifest.NotificationTypes),
		}
	}

	for _, r := range routeTable.All() {
		hm, ok := hashModules[r.Entry.ModuleName]
		if !ok {
			continue // engine built-in (ModuleName == "") or a failed module
		}
		mf := r.Entry.Manifest
		hm.Routes = append(hm.Routes, schemaHashRoute{
			Method:         r.Method,
			Path:           r.Entry.PathTemplate,
			Permissions:    mf.Permissions,
			Model:          mf.Model,
			CrudAction:     mf.CrudAction,
			Name:           mf.Name,
			ResponseIsList: mf.ResponseIsList,
			RequestType:    mf.RequestType,
			ResponseType:   mf.ResponseType,
		})
		hashModules[r.Entry.ModuleName] = hm
	}

	// The engine's own notification types are part of the response too,
	// and change with the binary, so an engine upgrade that changes them
	// changes the hash even when no module does.
	data, err := json.Marshal(struct {
		Modules                 map[string]schemaHashModule
		EngineNotificationTypes []SchemaNotificationType
	}{hashModules, schemaNotificationTypesFrom(enginenotif.Types)}, json.Deterministic(true))
	if err != nil {
		// hashModules contains only strings, bools, slices, and maps of
		// those — never a channel, func, or cyclic value — so
		// json.Marshal cannot fail on it in practice.
		panic(fmt.Sprintf("computeSchemaHash: marshal: %v", err))
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func buildRouteTable(modules map[string]*module.LoadedModule) (*route.RouteTable, error) {
	table := route.New()
	registerBuiltinRoutes(table)
	for name, m := range modules {
		if m.Status == module.StatusFailed {
			continue
		}
		explicit := route.ExplicitRoutesFrom(m.ExplicitRoutes)
		suppressed, err := route.RegisterRoutes(table, name, m.Manifest.Type, explicit, m.ModelDecls)
		if err != nil {
			return nil, fmt.Errorf("module %q: %w", name, err)
		}
		for _, s := range suppressed {
			log.Warn().Str("module", name).Str("model", s.Model).Str("op", s.Op).
				Msg(s.LogMessage())
		}
	}
	return table, nil
}

// Builtin routes share the module route table and use reserved namespaces. Tenant-facing
// /admin paths remain distinct from the operator API; metadata routes require standard
// identity middleware.
func registerBuiltinRoutes(table *route.RouteTable) {
	for _, path := range []string{"/_health", "/_ready"} {
		table.Register("GET", path, &route.RouteEntry{
			Manifest:     route.RouteManifest{EngineNative: true, EngineBuiltin: true},
			PathTemplate: path,
		})
	}
	for _, path := range []string{
		"/auth/login", "/auth/handoff", "/auth/select-tenant", "/auth/mfa/verify", "/auth/mfa/reverify",
		"/auth/mfa/enroll/totp", "/auth/mfa/enroll/totp/confirm",
		"/auth/mfa/enroll/webauthn", "/auth/mfa/enroll/webauthn/confirm",
		"/auth/mfa/webauthn/options", "/auth/mfa/reverify/webauthn/options",
		"/admin/users/{id}/mfa/reset", "/admin/users/{id}/roles",
		"/auth/refresh", "/auth/logout", "/admin/tenant/plan",
		"/auth/password-reset/request", "/auth/password-reset/confirm",
		"/auth/verify-email", "/auth/verify-email/resend",
		"/auth/me/change-password", "/auth/accept-invite",
	} {
		table.Register("POST", path, &route.RouteEntry{
			Manifest:     route.RouteManifest{EngineNative: true, EngineBuiltin: true},
			PathTemplate: path,
		})
	}

	// Registration provisions a tenant per success, so both registration
	// routes carry their own per-IP limits (auth-internals.md §15).
	table.Register("POST", "/auth/register", &route.RouteEntry{
		Manifest: route.RouteManifest{
			EngineNative: true, EngineBuiltin: true,
			RateLimit: &route.RateLimitConfig{Requests: 5, WindowSeconds: 3600, Scope: "ip"},
		},
		PathTemplate: "/auth/register",
	})

	table.Register("GET", "/auth/me", &route.RouteEntry{
		Manifest:     route.RouteManifest{EngineNative: true, EngineBuiltin: true},
		PathTemplate: "/auth/me",
	})
	// Anonymous slug-availability check for the register page.
	table.Register("GET", "/auth/check-slug", &route.RouteEntry{
		Manifest: route.RouteManifest{
			EngineNative: true, EngineBuiltin: true,
			RateLimit: &route.RateLimitConfig{Requests: 60, WindowSeconds: 60, Scope: "ip"},
		},
		PathTemplate: "/auth/check-slug",
	})
	// Anonymous accept-invite prefill (Class B: tenant from ?tenant=).
	table.Register("GET", "/auth/accept-invite/info", &route.RouteEntry{
		Manifest:     route.RouteManifest{EngineNative: true, EngineBuiltin: true},
		PathTemplate: "/auth/accept-invite/info",
	})
	// Anonymous pre-login tenant lookup for the shell's login page; resolves
	// the tenant from Host itself, like /auth/me.
	table.Register("GET", "/auth/tenant-context", &route.RouteEntry{
		Manifest:     route.RouteManifest{EngineNative: true, EngineBuiltin: true},
		PathTemplate: "/auth/tenant-context",
	})
	// Inbound connector webhooks (connector-guide.md §3): the URL token is the
	// sender's only identity, and the handler limits requests per token
	// itself, since providers call from many IPs.
	table.Register("POST", "/_webhooks/{module_name}/{token}", &route.RouteEntry{
		Manifest:     route.RouteManifest{EngineNative: true, EngineBuiltin: true, OwnRateLimit: true},
		PathTemplate: "/_webhooks/{module_name}/{token}",
	})
	table.Register("PATCH", "/auth/me", &route.RouteEntry{
		Manifest:     route.RouteManifest{EngineNative: true, EngineBuiltin: true},
		PathTemplate: "/auth/me",
	})
	table.Register("DELETE", "/admin/users/{id}/roles/{role}", &route.RouteEntry{
		Manifest:     route.RouteManifest{EngineNative: true, EngineBuiltin: true},
		PathTemplate: "/admin/users/{id}/roles/{role}",
	})
	for _, r := range [][2]string{
		{"GET", "/auth/sessions"},
		{"DELETE", "/auth/sessions"},
		{"DELETE", "/auth/sessions/{family_id}"},
	} {
		table.Register(r[0], r[1], &route.RouteEntry{
			Manifest:     route.RouteManifest{EngineNative: true, EngineBuiltin: true},
			PathTemplate: r[1],
		})
	}
	for _, r := range [][2]string{
		{"GET", "/auth/mfa/factors"},
		{"POST", "/auth/mfa/factors/{id}/remove"},
		{"POST", "/auth/mfa/recovery-codes/regenerate"},
	} {
		table.Register(r[0], r[1], &route.RouteEntry{
			Manifest:     route.RouteManifest{EngineNative: true, EngineBuiltin: true},
			PathTemplate: r[1],
		})
	}
	// Tenant-facing admin handlers resolve identity themselves and require the middleware
	// bypass.
	for _, r := range [][2]string{
		{"GET", "/admin/users"},
		{"GET", "/admin/users/{id}"},
		{"DELETE", "/admin/users/{id}"},
		{"POST", "/admin/users/{id}/suspend"},
		{"POST", "/admin/users/{id}/unsuspend"},
		{"GET", "/admin/users/{id}/sessions"},
		{"GET", "/admin/users/{id}/activity"},
		{"DELETE", "/admin/users/{id}/sessions/{family_id}"},
		{"POST", "/users/invite"},
		{"POST", "/users/invitations/{id}/resend"},
		{"POST", "/users/invitations/{id}/revoke"},
		{"GET", "/admin/roles"},
		{"POST", "/admin/roles"},
		{"GET", "/admin/roles/permissions"},
		{"GET", "/admin/roles/{id}"},
		{"PATCH", "/admin/roles/{id}"},
		{"DELETE", "/admin/roles/{id}"},
		{"GET", "/admin/settings"},
		{"PATCH", "/admin/settings"},
		{"POST", "/admin/settings/logo"},
		{"DELETE", "/admin/settings/logo"},
		{"GET", "/admin/settings/notification-delivery"},
		{"PATCH", "/admin/settings/notification-delivery"},
		{"POST", "/admin/settings/notification-delivery/test-email"},
		{"GET", "/admin/settings/notification-templates"},
		{"GET", "/admin/settings/notification-templates/{type}/{channel}/{locale}"},
		{"PUT", "/admin/settings/notification-templates/{type}/{channel}/{locale}"},
		{"DELETE", "/admin/settings/notification-templates/{type}/{channel}/{locale}"},
		{"POST", "/admin/settings/notification-templates/{type}/{channel}/{locale}/preview"},
		{"PATCH", "/admin/connectors/{name}/set-primary"},
		{"GET", "/admin/modules"},
		{"GET", "/admin/modules/{name}"},
		{"PATCH", "/admin/modules/{name}/settings"},
		{"GET", "/admin/modules/{name}/cron-jobs"},
		{"PATCH", "/admin/modules/{name}/cron-jobs/{cron_name}"},
		{"GET", "/admin/connectors"},
		{"GET", "/admin/connectors/{name}"},
		{"PATCH", "/admin/config"},
		{"POST", "/admin/connectors/{name}/config/{key}/rotate"},
		{"DELETE", "/admin/connectors/{name}/webhook"},
	} {
		table.Register(r[0], r[1], &route.RouteEntry{
			Manifest:     route.RouteManifest{EngineNative: true, EngineBuiltin: true},
			PathTemplate: r[1],
		})
	}
	table.Register("GET", "/_meta/permissions", &route.RouteEntry{
		Manifest:     route.RouteManifest{EngineNative: true, Auth: "required"},
		PathTemplate: "/_meta/permissions",
	})

	table.Register("GET", "/_meta/schema", &route.RouteEntry{
		Manifest:     route.RouteManifest{EngineNative: true, Auth: "required"},
		PathTemplate: "/_meta/schema",
	})

	// WebSocket upgrades require a resolved tenant and user from the standard middleware
	// chain.
	table.Register("GET", "/_ws", &route.RouteEntry{
		Manifest:     route.RouteManifest{EngineNative: true, Auth: "required"},
		PathTemplate: "/_ws",
	})

	// Share handlers rely on standard tenant/auth middleware for identity resolution.
	table.Register("POST", "/_meta/shares", &route.RouteEntry{
		Manifest:     route.RouteManifest{EngineNative: true, Auth: "required"},
		PathTemplate: "/_meta/shares",
	})
	table.Register("GET", "/_meta/shares", &route.RouteEntry{
		Manifest:     route.RouteManifest{EngineNative: true, Auth: "required"},
		PathTemplate: "/_meta/shares",
	})
	table.Register("DELETE", "/_meta/shares/{id}", &route.RouteEntry{
		Manifest: route.RouteManifest{
			EngineNative: true,
			Auth:         "required",
			PathParams:   map[string]string{"id": "uuid"},
		},
		PathTemplate: "/_meta/shares/{id}",
	})

	table.Register("POST", "/_meta/saved-filters", &route.RouteEntry{
		Manifest:     route.RouteManifest{EngineNative: true, Auth: "required"},
		PathTemplate: "/_meta/saved-filters",
	})
	table.Register("GET", "/_meta/saved-filters", &route.RouteEntry{
		Manifest:     route.RouteManifest{EngineNative: true, Auth: "required"},
		PathTemplate: "/_meta/saved-filters",
	})
	table.Register("PATCH", "/_meta/saved-filters/{id}", &route.RouteEntry{
		Manifest: route.RouteManifest{
			EngineNative: true,
			Auth:         "required",
			PathParams:   map[string]string{"id": "uuid"},
		},
		PathTemplate: "/_meta/saved-filters/{id}",
	})
	table.Register("DELETE", "/_meta/saved-filters/{id}", &route.RouteEntry{
		Manifest: route.RouteManifest{
			EngineNative: true,
			Auth:         "required",
			PathParams:   map[string]string{"id": "uuid"},
		},
		PathTemplate: "/_meta/saved-filters/{id}",
	})

	table.Register("GET", "/_meta/activity", &route.RouteEntry{
		Manifest:     route.RouteManifest{EngineNative: true, Auth: "required"},
		PathTemplate: "/_meta/activity",
	})
	table.Register("POST", "/_meta/activity", &route.RouteEntry{
		Manifest:     route.RouteManifest{EngineNative: true, Auth: "required"},
		PathTemplate: "/_meta/activity",
	})
	table.Register("DELETE", "/_meta/activity/{id}", &route.RouteEntry{
		Manifest: route.RouteManifest{
			EngineNative: true,
			Auth:         "required",
			PathParams:   map[string]string{"id": "uuid"},
		},
		PathTemplate: "/_meta/activity/{id}",
	})
	// The static followers segment wins over {id} in the route tree.
	for _, method := range []string{"GET", "PUT", "DELETE"} {
		table.Register(method, "/_meta/activity/followers", &route.RouteEntry{
			Manifest:     route.RouteManifest{EngineNative: true, Auth: "required"},
			PathTemplate: "/_meta/activity/followers",
		})
	}

	table.Register("GET", "/_meta/record-readers", &route.RouteEntry{
		Manifest:     route.RouteManifest{EngineNative: true, Auth: "required"},
		PathTemplate: "/_meta/record-readers",
	})

	// Static notification segments must win over the dynamic id segment.
	for _, r := range [][2]string{
		{"GET", "/_notif/feed"},
		{"GET", "/_notif/count"},
		{"POST", "/_notif/read-all"},
		{"DELETE", "/_notif/all"},
		{"POST", "/_notif/device-token"},
		{"GET", "/_notif/preferences"},
		{"PATCH", "/_notif/preferences"},
	} {
		table.Register(r[0], r[1], &route.RouteEntry{
			Manifest:     route.RouteManifest{EngineNative: true, Auth: "required"},
			PathTemplate: r[1],
		})
	}
	for _, r := range [][2]string{
		{"POST", "/_notif/{id}/read"},
		{"DELETE", "/_notif/{id}"},
	} {
		table.Register(r[0], r[1], &route.RouteEntry{
			Manifest: route.RouteManifest{
				EngineNative: true,
				Auth:         "required",
				PathParams:   map[string]string{"id": "uuid"},
			},
			PathTemplate: r[1],
		})
	}

	// The email unsubscribe link (notification-system.md §10): GET asks
	// for confirmation, POST unsubscribes. Both resolve their own tenant
	// from Host and their user from the signed token, with no session, so
	// unlike the rest of /_notif they are EngineBuiltin.
	for _, method := range []string{"GET", "POST"} {
		table.Register(method, "/_notif/unsubscribe", &route.RouteEntry{
			Manifest:     route.RouteManifest{EngineNative: true, EngineBuiltin: true},
			PathTemplate: "/_notif/unsubscribe",
		})
	}

	table.Register("GET", "/_meta/scheduled-activities", &route.RouteEntry{
		Manifest:     route.RouteManifest{EngineNative: true, Auth: "required"},
		PathTemplate: "/_meta/scheduled-activities",
	})
	table.Register("GET", "/_meta/scheduled-activities/mine", &route.RouteEntry{
		Manifest:     route.RouteManifest{EngineNative: true, Auth: "required"},
		PathTemplate: "/_meta/scheduled-activities/mine",
	})
	table.Register("POST", "/_meta/scheduled-activities", &route.RouteEntry{
		Manifest:     route.RouteManifest{EngineNative: true, Auth: "required"},
		PathTemplate: "/_meta/scheduled-activities",
	})
	table.Register("PATCH", "/_meta/scheduled-activities/{id}", &route.RouteEntry{
		Manifest: route.RouteManifest{
			EngineNative: true,
			Auth:         "required",
			PathParams:   map[string]string{"id": "uuid"},
		},
		PathTemplate: "/_meta/scheduled-activities/{id}",
	})
	table.Register("POST", "/_meta/scheduled-activities/{id}/done", &route.RouteEntry{
		Manifest: route.RouteManifest{
			EngineNative: true,
			Auth:         "required",
			PathParams:   map[string]string{"id": "uuid"},
		},
		PathTemplate: "/_meta/scheduled-activities/{id}/done",
	})
	table.Register("DELETE", "/_meta/scheduled-activities/{id}", &route.RouteEntry{
		Manifest: route.RouteManifest{
			EngineNative: true,
			Auth:         "required",
			PathParams:   map[string]string{"id": "uuid"},
		},
		PathTemplate: "/_meta/scheduled-activities/{id}",
	})

	// /_meta/activity-types and /admin/activity-types
	// (scheduled-activities.md §9) — same posture as /_meta/activity
	// above. The admin routes check the admin role themselves; the static
	// "order" segment wins over {key} in the route tree.
	for _, r := range [][2]string{
		{"GET", "/_meta/activity-types"},
		{"GET", "/admin/activity-types"},
		{"POST", "/admin/activity-types"},
		{"PATCH", "/admin/activity-types/{key}"},
		{"PUT", "/admin/activity-types/order"},
		{"DELETE", "/admin/activity-types/{key}"},
	} {
		table.Register(r[0], r[1], &route.RouteEntry{
			Manifest:     route.RouteManifest{EngineNative: true, Auth: "required"},
			PathTemplate: r[1],
		})
	}

	// The upload handler resolves tenant/auth itself, so standard middleware must not
	// duplicate that work.
	table.Register("POST", "/storage/upload", &route.RouteEntry{
		Manifest:     route.RouteManifest{EngineNative: true, EngineBuiltin: true},
		PathTemplate: "/storage/upload",
	})

	// Frontend bundles are anonymous module code, independent of tenant identity.
	table.Register("GET", "/modules/{module}/frontend/{file}", &route.RouteEntry{
		Manifest:     route.RouteManifest{EngineNative: true, EngineBuiltin: true},
		PathTemplate: "/modules/{module}/frontend/{file}",
	})

	table.Register("GET", "/modules/{module}/translations/{file}", &route.RouteEntry{
		Manifest:     route.RouteManifest{EngineNative: true, EngineBuiltin: true},
		PathTemplate: "/modules/{module}/translations/{file}",
	})
}

func buildEventRegistry(modules map[string]*module.LoadedModule) *event.EventRegistry {
	reg := event.NewEventRegistry()
	for name, m := range modules {
		if m.Status == module.StatusFailed {
			continue
		}
		reg.Register(name, m.Manifest)
	}
	return reg
}

func buildPermissionRegistry(modules map[string]*module.LoadedModule) *permission.PermissionRegistry {
	reg := permission.NewPermissionRegistry()
	for name, m := range modules {
		if m.Status == module.StatusFailed {
			continue
		}
		reg.Register(name, m.Manifest.Permissions)
	}
	return reg
}

// Job names must be unique across loaded modules. The loader isolates conflicting modules;
// rebuilding still returns an error if a collision survives.
func buildJobRegistry(modules map[string]*module.LoadedModule) (*job.JobRegistry, error) {
	reg := job.New()
	for name, m := range modules {
		if m.Status == module.StatusFailed {
			continue
		}
		if err := reg.Register(name, m.Manifest.JobTypes); err != nil {
			return nil, fmt.Errorf("module %q: %w", name, err)
		}
	}
	return reg, nil
}
