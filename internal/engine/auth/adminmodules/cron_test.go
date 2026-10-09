package adminmodules

import (
	"bytes"
	"context"
	"encoding/json/v2"
	"maps"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
	"time"
	"uuid"

	"github.com/djangbahevans/goerp/internal/engine/billing"
	"github.com/djangbahevans/goerp/internal/engine/cronsettings"
	"github.com/djangbahevans/goerp/internal/engine/db"
	"github.com/djangbahevans/goerp/internal/engine/manifest"
	"github.com/djangbahevans/goerp/internal/engine/mfa/enforce"
	"github.com/djangbahevans/goerp/internal/engine/module"
	"github.com/djangbahevans/goerp/internal/engine/registry"
	"github.com/djangbahevans/goerp/internal/engine/route"
	tenantresolve "github.com/djangbahevans/goerp/internal/engine/tenant/resolve"
	"github.com/djangbahevans/goerp/internal/engine/tenantconfig"
	"github.com/djangbahevans/goerp/internal/engine/tenantschema"
)

type cronListJSON struct {
	Module        string        `json:"module"`
	Entitled      bool          `json:"entitled"`
	ModuleEnabled bool          `json:"module_enabled"`
	ModuleReady   bool          `json:"module_ready"`
	CronJobs      []cronJobJSON `json:"cron_jobs"`
}

func TestCronLiveAdminRoleAndMFARestrictions(t *testing.T) {
	e, ft, token := cronEnv(t)
	policy := enforce.NewStore(tenantconfig.NewStore(e.conn))
	if err := policy.SavePolicy(t.Context(), ft.id, enforce.Policy{Mode: enforce.ModeRequired, MaxAssuranceAge: time.Hour}); err != nil {
		t.Fatal(err)
	}
	requireStatus(t, cronRequest(t, e, ft, token, modApp, "", nil), http.StatusForbidden, "mfa_setup_required")
	requireStatus(t, cronRequest(t, e, ft, token, modApp, "opt_in", map[string]any{"enabled": true, "expected_generation": uuid.NewV7().String()}), http.StatusForbidden, "mfa_setup_required")
	if err := policy.SavePolicy(t.Context(), ft.id, enforce.Policy{Mode: enforce.ModeOptional, MaxAssuranceAge: time.Hour}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.conn.ExecContext(t.Context(), "UPDATE "+tenantschema.Name(ft.slug)+".user_roles SET role_id = (SELECT id FROM "+tenantschema.Name(ft.slug)+".roles WHERE name = 'user')"); err != nil {
		t.Fatal(err)
	}
	requireStatus(t, cronRequest(t, e, ft, token, modApp, "", nil), http.StatusForbidden, "forbidden")
}

func cronEnv(t *testing.T) (*env, fixtureTenant, string) {
	t.Helper()
	e := newEnv(t)
	ft := e.newTenant(t)
	store := cronsettings.NewStore(e.conn)
	e.handler.Cron = store
	reg := e.handler.Registry.(*registry.ModuleRegistry)
	modules := maps.Clone(reg.Snapshot().Modules())
	for name, existing := range modules {
		m := *existing
		m.Manifest.CronJobs = []manifest.CronJob{
			{Name: "opt_in", Label: "Opt in", Schedule: "* * * * *", Handler: "opt_in", EnabledByDefault: new(false)},
			{Name: "digest", Label: "Digest", Schedule: "0 3 * * *", Handler: "digest"},
		}
		modules[name] = &m
		if err := store.Initialize(t.Context(), ft.slug, name, m.Manifest.CronJobs); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := reg.Update(modules); err != nil {
		t.Fatal(err)
	}
	return e, ft, e.token(t, ft, "admin")
}

func cronRequest(t *testing.T, e *env, ft fixtureTenant, token, module, job string, body any) *httptest.ResponseRecorder {
	t.Helper()
	if job == "" {
		return do(t, ft, token, e.handler.ServeCronList, http.MethodGet, "/admin/modules/"+module+"/cron-jobs", module, nil)
	}
	var buf bytes.Buffer
	if err := json.MarshalWrite(&buf, body); err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodPatch, "/admin/modules/"+module+"/cron-jobs/"+job, &buf)
	r.Host = ft.domain
	r.Header.Set("Authorization", "Bearer "+token)
	r = r.WithContext(route.WithParams(r.Context(), map[string]string{"name": module, "cron_name": job}))
	rec := httptest.NewRecorder()
	e.handler.ServeCronPatch(rec, r)
	return rec
}

func TestCronListAndExplicitSetter(t *testing.T) {
	e, ft, token := cronEnv(t)
	rec := cronRequest(t, e, ft, token, modApp, "", nil)
	requireStatus(t, rec, http.StatusOK, "")
	list := decode[cronListJSON](t, rec)
	if list.Module != modApp || !list.Entitled || !list.ModuleEnabled || !list.ModuleReady || len(list.CronJobs) != 2 {
		t.Fatalf("cron list = %+v", list)
	}
	job := list.CronJobs[0]
	if job.Name != "opt_in" || job.Enabled || job.EffectiveEnabled || job.BlockedReason == nil || *job.BlockedReason != "job_disabled" || job.NextRunAt != nil || job.Description != "" || job.Queue != "bulk" || job.TimeoutSeconds != 3600 {
		t.Fatalf("disabled cron = %+v", job)
	}
	if next := list.CronJobs[1].NextRunAt; next == nil || !next.After(time.Now().Add(-time.Second)) || next.Location() != time.UTC {
		t.Fatalf("next run = %v", next)
	}

	rec = cronRequest(t, e, ft, token, modApp, job.Name, map[string]any{"enabled": true, "expected_generation": job.Generation})
	requireStatus(t, rec, http.StatusOK, "")
	enabled := decode[cronJobJSON](t, rec)
	if !enabled.Enabled || !enabled.EffectiveEnabled || enabled.Generation == job.Generation || enabled.NextRunAt == nil {
		t.Fatalf("enabled response = %+v", enabled)
	}
	if n := e.auditCount(t, ft, "cron.enabled"); n != 1 {
		t.Fatalf("enabled audits = %d", n)
	}
	rec = cronRequest(t, e, ft, token, modApp, job.Name, map[string]any{"enabled": true, "expected_generation": job.Generation})
	requireStatus(t, rec, http.StatusOK, "")
	if same := decode[cronJobJSON](t, rec); same.Generation != enabled.Generation || !same.UpdatedAt.Equal(enabled.UpdatedAt) || e.auditCount(t, ft, "cron.enabled") != 1 {
		t.Fatalf("same-state request changed audit/generation: %+v", same)
	}
	rec = cronRequest(t, e, ft, token, modApp, job.Name, map[string]any{"enabled": false, "expected_generation": job.Generation})
	requireStatus(t, rec, http.StatusConflict, "cron_settings_conflict")
}

type afterCommitCronSettings struct {
	CronSettings
	afterCommit func()
	committed   cronsettings.State
	reads       int
}

func (s *afterCommitCronSettings) Read(ctx context.Context, slug string, identities []cronsettings.Identity) (map[cronsettings.Identity]cronsettings.State, error) {
	s.reads++

	return s.CronSettings.Read(ctx, slug, identities)
}

func (s *afterCommitCronSettings) SetEnabled(ctx context.Context, tenantID, slug string, identity cronsettings.Identity, enabled bool, expectedGeneration, actor string) (cronsettings.State, error) {
	state, err := s.CronSettings.SetEnabled(ctx, tenantID, slug, identity, enabled, expectedGeneration, actor)
	if err != nil {
		return state, err
	}

	s.committed = state
	s.afterCommit()

	return state, nil
}

func TestCronSetterAcknowledgesCommitDespiteEligibilityChanges(t *testing.T) {
	for _, change := range []string{"reload", "declaration_removed", "module_removed", "entitlements_unavailable"} {
		t.Run(change, func(t *testing.T) {
			e, ft, token := cronEnv(t)
			rec := cronRequest(t, e, ft, token, modApp, "", nil)
			requireStatus(t, rec, http.StatusOK, "")
			initial := decode[cronListJSON](t, rec).CronJobs[0]

			settings := &afterCommitCronSettings{CronSettings: e.handler.Cron}
			settings.afterCommit = func() {
				if change == "entitlements_unavailable" {
					conn, err := db.New(localPostgresDSN)
					if err != nil {
						t.Fatal(err)
					}
					if err := conn.Close(); err != nil {
						t.Fatal(err)
					}

					e.handler.Tenants = tenantresolve.NewResolver(e.tenants, e.handler.Cache, billing.NewStore(conn))
					return
				}

				reg := e.handler.Registry.(*registry.ModuleRegistry)
				modules := maps.Clone(reg.Snapshot().Modules())
				m := *modules[modApp]
				m.Manifest.CronJobs = slices.Clone(m.Manifest.CronJobs)
				switch change {
				case "reload":
					m.Status = module.StatusFailed
					m.Manifest.CronJobs[0].Label = "Reloaded job"
					m.Manifest.CronJobs[0].Schedule = "0 4 * * *"
				case "declaration_removed":
					m.Manifest.CronJobs = m.Manifest.CronJobs[1:]
				case "module_removed":
					delete(modules, modApp)
				}
				if change != "module_removed" {
					modules[modApp] = &m
				}
				if _, err := reg.Update(modules); err != nil {
					t.Fatal(err)
				}
			}
			e.handler.Cron = settings

			rec = cronRequest(t, e, ft, token, modApp, initial.Name, map[string]any{"enabled": true, "expected_generation": initial.Generation})
			requireStatus(t, rec, http.StatusOK, "")
			got := decode[cronJobJSON](t, rec)
			if !got.Enabled || !got.EffectiveEnabled || got.BlockedReason != nil || got.NextRunAt == nil || got.Label != initial.Label || got.Schedule != initial.Schedule {
				t.Fatalf("acknowledgement lost request metadata: %+v", got)
			}
			if got.Generation != settings.committed.Generation || !got.UpdatedAt.Equal(settings.committed.UpdatedAt) || got.Generation == initial.Generation {
				t.Fatalf("acknowledgement state = %+v, committed = %+v", got, settings.committed)
			}
			if settings.reads != 0 {
				t.Fatalf("setter performed %d additional settings reads", settings.reads)
			}

			states, err := cronsettings.NewStore(e.conn).Read(t.Context(), ft.slug, []cronsettings.Identity{{Module: modApp, Name: initial.Name}})
			if err != nil {
				t.Fatal(err)
			}
			if persisted := states[cronsettings.Identity{Module: modApp, Name: initial.Name}]; persisted != settings.committed {
				t.Fatalf("persisted state = %+v, committed = %+v", persisted, settings.committed)
			}
			if n := e.auditCount(t, ft, "cron.enabled"); n != 1 {
				t.Fatalf("committed toggle audit count = %d", n)
			}

			if change == "entitlements_unavailable" {
				if _, err := e.handler.Tenants.LoadCurrentEntitlements(t.Context(), ft.id); err == nil {
					t.Fatal("second entitlement lookup would succeed")
				}
			}
		})
	}
}

func TestCronRequestValidationAndAuthorization(t *testing.T) {
	e, ft, token := cronEnv(t)
	for _, body := range []any{
		map[string]any{},
		map[string]any{"enabled": true},
		map[string]any{"enabled": nil, "expected_generation": uuid.NewV7().String()},
		map[string]any{"enabled": "true", "expected_generation": uuid.NewV7().String()},
		map[string]any{"enabled": true, "expected_generation": nil},
		map[string]any{"enabled": true, "expected_generation": "bad"},
		map[string]any{"enabled": true, "expected_generation": uuid.NewV7().String(), "schedule": "* * * * *"},
	} {
		requireStatus(t, cronRequest(t, e, ft, token, modApp, "opt_in", body), http.StatusBadRequest, "invalid_request")
	}
	for _, auth := range []struct {
		token  string
		status int
		code   string
	}{
		{"", http.StatusUnauthorized, "unauthenticated"},
		{"operator-token", http.StatusUnauthorized, "unauthenticated"},
		{e.token(t, ft, "user"), http.StatusForbidden, "forbidden"},
		{e.token(t, e.newTenant(t), "admin"), http.StatusUnauthorized, "unauthenticated"},
	} {
		requireStatus(t, cronRequest(t, e, ft, auth.token, modApp, "", nil), auth.status, auth.code)
		requireStatus(t, cronRequest(t, e, ft, auth.token, modApp, "opt_in", map[string]any{"enabled": true, "expected_generation": uuid.NewV7().String()}), auth.status, auth.code)
	}
	requireStatus(t, cronRequest(t, e, ft, token, "unknown", "", nil), http.StatusNotFound, "not_found")
	requireStatus(t, cronRequest(t, e, ft, token, modApp, "unknown", map[string]any{"enabled": true, "expected_generation": uuid.NewV7().String()}), http.StatusNotFound, "not_found")
}

func TestCronBlockedStateAndMissingSettings(t *testing.T) {
	e, ft, token := cronEnv(t)
	rec := cronRequest(t, e, ft, token, modPremium, "", nil)
	requireStatus(t, rec, http.StatusOK, "")
	list := decode[cronListJSON](t, rec)
	if list.Entitled || *list.CronJobs[0].BlockedReason != "module_not_entitled" {
		t.Fatalf("premium list = %+v", list)
	}
	requireStatus(t, cronRequest(t, e, ft, token, modPremium, "opt_in", map[string]any{"enabled": true, "expected_generation": list.CronJobs[0].Generation}), http.StatusConflict, "module_not_entitled")

	if err := e.billing.SetModuleEnabledForTenant(t.Context(), ft.id, modApp, false, nil); err != nil {
		t.Fatal(err)
	}
	rec = cronRequest(t, e, ft, token, modApp, "", nil)
	list = decode[cronListJSON](t, rec)
	if list.ModuleEnabled || *list.CronJobs[0].BlockedReason != "module_disabled" {
		t.Fatalf("disabled module list = %+v", list)
	}
	rec = cronRequest(t, e, ft, token, modApp, "opt_in", map[string]any{"enabled": true, "expected_generation": list.CronJobs[0].Generation})
	requireStatus(t, rec, http.StatusOK, "")
	job := decode[cronJobJSON](t, rec)
	if !job.Enabled || job.EffectiveEnabled || *job.BlockedReason != "module_disabled" {
		t.Fatalf("saved choice on disabled module = %+v", job)
	}

	reg := e.handler.Registry.(*registry.ModuleRegistry)
	modules := maps.Clone(reg.Snapshot().Modules())
	m := *modules[modApp]
	m.Status = module.StatusFailed
	modules[modApp] = &m
	if _, err := reg.Update(modules); err != nil {
		t.Fatal(err)
	}
	rec = cronRequest(t, e, ft, token, modApp, "", nil)
	list = decode[cronListJSON](t, rec)
	if list.ModuleReady || *list.CronJobs[0].BlockedReason != "module_not_ready" {
		t.Fatalf("nonready list = %+v", list)
	}
	requireStatus(t, cronRequest(t, e, ft, token, modApp, "opt_in", map[string]any{"enabled": false, "expected_generation": job.Generation}), http.StatusConflict, "module_not_ready")

	if err := cronsettings.NewStore(e.conn).RemoveModule(t.Context(), modApp); err != nil {
		t.Fatal(err)
	}
	requireStatus(t, cronRequest(t, e, ft, token, modApp, "", nil), http.StatusServiceUnavailable, "cron_settings_unavailable")
}

func TestModuleListIncludesUnavailableDeclarationsAndExcludesInvalidArtifacts(t *testing.T) {
	e := newEnv(t)
	ft := e.newTenant(t)
	token := e.token(t, ft, "admin")
	reg := e.handler.Registry.(*registry.ModuleRegistry)
	modules := maps.Clone(reg.Snapshot().Modules())
	unavailable := *modules[modApp]
	unavailable.Status = module.StatusFailed
	modules[modApp] = &unavailable
	modules["invalid_manifest"] = &module.LoadedModule{Status: module.StatusFailed}
	if _, err := reg.Update(modules); err != nil {
		t.Fatal(err)
	}

	rec := do(t, ft, token, e.handler.ServeList, http.MethodGet, "/admin/modules", "", nil)
	requireStatus(t, rec, http.StatusOK, "")
	list := decode[struct {
		Modules []moduleJSON `json:"modules"`
	}](t, rec)

	if !slices.ContainsFunc(list.Modules, func(m moduleJSON) bool { return m.Name == modApp }) {
		t.Fatal("installed unavailable module disappeared from tenant controls")
	}
	if slices.ContainsFunc(list.Modules, func(m moduleJSON) bool { return m.Name == "" }) {
		t.Fatal("invalid artifact appeared without a tenant module identity")
	}

	for _, status := range []module.ModuleStatus{module.StatusFailed, module.StatusSyncing, module.StatusDraining} {
		m := unavailable
		m.Status = status
		modules[modApp] = &m
		premium := *modules[modPremium]
		premium.Status = status
		modules[modPremium] = &premium
		if _, err := reg.Update(modules); err != nil {
			t.Fatal(err)
		}

		rec = e.get(t, ft, token, modApp)
		requireStatus(t, rec, http.StatusOK, "")
		detail := decode[moduleDetailJSON](t, rec)
		if detail.Name != modApp || !detail.Entitled || len(detail.Config) != len(unavailable.Manifest.ConfigSchema) {
			t.Fatalf("%s module detail = %+v", status, detail)
		}

		rec = e.get(t, ft, token, modPremium)
		requireStatus(t, rec, http.StatusOK, "")
		detail = decode[moduleDetailJSON](t, rec)
		if detail.Name != modPremium || detail.Entitled || detail.Config == nil || len(detail.Config) != 0 {
			t.Fatalf("%s unentitled module detail = %+v", status, detail)
		}
	}

	requireStatus(t, e.get(t, ft, token, "invalid_manifest"), http.StatusNotFound, "not_found")
	requireStatus(t, e.get(t, ft, token, "unknown"), http.StatusNotFound, "not_found")
	requireStatus(t, cronRequest(t, e, ft, token, "invalid_manifest", "", nil), http.StatusNotFound, "not_found")
}
