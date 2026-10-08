package cronsched

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/jobqueue"
	"github.com/djangbahevans/goerp/internal/engine/manifest"
	"github.com/djangbahevans/goerp/internal/engine/module"
	"github.com/djangbahevans/goerp/internal/engine/registry"
	"github.com/djangbahevans/goerp/internal/engine/tenant"
	tenantresolve "github.com/djangbahevans/goerp/internal/engine/tenant/resolve"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
)

type fakeTenants []tenant.Tenant

func (f fakeTenants) ActiveTenants(context.Context) ([]tenant.Tenant, error) { return f, nil }

// fakeEntitlements maps a tenant ID to the modules it has enabled.
type fakeEntitlements map[string][]string

func (f fakeEntitlements) LoadEntitlements(_ context.Context, tenantID string) (tenantresolve.EntitlementSet, error) {
	modules, ok := f[tenantID]
	if !ok {
		return tenantresolve.EntitlementSet{}, errors.New("entitlements unavailable")
	}
	features := map[string]bool{}
	for _, m := range modules {
		features["module."+m] = true
	}
	return tenantresolve.EntitlementSet{Features: features}, nil
}

type fakeInserter struct {
	inserted []jobqueue.WASMJobArgs
}

func (f *fakeInserter) InsertMany(_ context.Context, params []river.InsertManyParams) ([]*rivertype.JobInsertResult, error) {
	for _, p := range params {
		f.inserted = append(f.inserted, p.Args.(jobqueue.WASMJobArgs))
	}
	return nil, nil
}

func readyModule(name string, jobs ...manifest.CronJob) *module.LoadedModule {
	return &module.LoadedModule{Status: module.StatusReady, Manifest: manifest.Manifest{Name: name, CronJobs: jobs}}
}

func cronJob(name, schedule string) manifest.CronJob {
	return manifest.CronJob{Name: name, Label: name, Schedule: schedule, Handler: name}
}

type env struct {
	reg      *registry.ModuleRegistry
	inserter *fakeInserter
	worker   *Worker
}

func newEnv(t *testing.T, modules map[string]*module.LoadedModule, entitlements fakeEntitlements, tenants ...string) *env {
	t.Helper()
	reg := &registry.ModuleRegistry{}
	if _, err := reg.Update(modules); err != nil {
		t.Fatalf("ModuleRegistry.Update: %v", err)
	}
	var ts fakeTenants
	for _, id := range tenants {
		ts = append(ts, tenant.Tenant{ID: id, Slug: "slug-" + id})
	}
	inserter := &fakeInserter{}
	return &env{reg: reg, inserter: inserter, worker: &Worker{Registry: reg, Tenants: ts, Entitlements: entitlements, Inserter: inserter}}
}

func (e *env) tick(t *testing.T, at string) error {
	t.Helper()
	parsed, err := time.Parse(time.DateTime, at)
	if err != nil {
		t.Fatal(err)
	}
	return e.worker.Work(t.Context(), &river.Job[jobqueue.CronTickArgs]{Args: jobqueue.CronTickArgs{At: parsed}})
}

func (e *env) firedTenants(jobType string) []string {
	var tenants []string
	for _, a := range e.inserter.inserted {
		if a.JobType == jobType {
			tenants = append(tenants, a.TenantID)
		}
	}
	slices.Sort(tenants)
	return tenants
}

func TestTickFiresDueCronJobForEachTenantWithTheModule(t *testing.T) {
	e := newEnv(t,
		map[string]*module.LoadedModule{"crm": readyModule("crm", cronJob("dedupe", "*/5 * * * *"))},
		fakeEntitlements{"t1": {"crm"}, "t2": {"crm"}, "t3": {"hr"}},
		"t1", "t2", "t3",
	)

	if err := e.tick(t, "2026-10-08 10:05:00"); err != nil {
		t.Fatalf("tick: %v", err)
	}

	if got, want := e.firedTenants("dedupe"), []string{"t1", "t2"}; !slices.Equal(got, want) {
		t.Errorf("fired for tenants %v, want %v (t3 has no crm module)", got, want)
	}
	a := e.inserter.inserted[0]
	if !a.IsCron || a.ModuleName != "crm" || a.Queue != "bulk" || a.MaxAttempts != maxAttempts || a.TraceID == "" || a.Payload != nil {
		t.Errorf("enqueued args = %+v, want a cron job for crm on bulk with a trace ID and no payload", a)
	}
}

func TestTickSkipsMinutesTheScheduleDoesNotMatch(t *testing.T) {
	e := newEnv(t,
		map[string]*module.LoadedModule{"crm": readyModule("crm", cronJob("dedupe", "*/5 * * * *"))},
		fakeEntitlements{"t1": {"crm"}}, "t1",
	)

	for _, at := range []string{"2026-10-08 10:04:00", "2026-10-08 10:06:00", "2026-10-08 10:07:00"} {
		if err := e.tick(t, at); err != nil {
			t.Fatalf("tick %s: %v", at, err)
		}
	}
	if len(e.inserter.inserted) != 0 {
		t.Errorf("enqueued %d jobs off-schedule, want none", len(e.inserter.inserted))
	}
}

func TestTickUsesTheCronJobsQueue(t *testing.T) {
	job := cronJob("digest", "0 8 * * *")
	job.Queue = "email"
	e := newEnv(t, map[string]*module.LoadedModule{"crm": readyModule("crm", job)}, fakeEntitlements{"t1": {"crm"}}, "t1")

	if err := e.tick(t, "2026-10-08 08:00:00"); err != nil {
		t.Fatal(err)
	}
	if len(e.inserter.inserted) != 1 || e.inserter.inserted[0].Queue != "email" {
		t.Errorf("enqueued = %+v, want one job on the email queue", e.inserter.inserted)
	}
}

func TestTickNeverFiresACronJobDisabledByDefault(t *testing.T) {
	off := false
	job := cronJob("opt_in", "* * * * *")
	job.EnabledByDefault = &off
	e := newEnv(t, map[string]*module.LoadedModule{"crm": readyModule("crm", job)}, fakeEntitlements{"t1": {"crm"}}, "t1")

	if err := e.tick(t, "2026-10-08 10:00:00"); err != nil {
		t.Fatal(err)
	}
	if len(e.inserter.inserted) != 0 {
		t.Errorf("enqueued %d jobs for enabled_by_default: false, want none", len(e.inserter.inserted))
	}
}

func TestTickSkipsModulesThatAreNotReady(t *testing.T) {
	failed := readyModule("crm", cronJob("dedupe", "* * * * *"))
	failed.Status = module.StatusFailed
	e := newEnv(t, map[string]*module.LoadedModule{"crm": failed}, fakeEntitlements{"t1": {"crm"}}, "t1")

	if err := e.tick(t, "2026-10-08 10:00:00"); err != nil {
		t.Fatal(err)
	}
	if len(e.inserter.inserted) != 0 {
		t.Errorf("enqueued %d jobs for a failed module, want none", len(e.inserter.inserted))
	}
}

func TestTickFollowsAReloadedSchedule(t *testing.T) {
	e := newEnv(t,
		map[string]*module.LoadedModule{"crm": readyModule("crm", cronJob("dedupe", "0 3 * * *"))},
		fakeEntitlements{"t1": {"crm"}}, "t1",
	)
	reload := func(jobs ...manifest.CronJob) {
		t.Helper()
		if _, err := e.reg.Update(map[string]*module.LoadedModule{"crm": readyModule("crm", jobs...)}); err != nil {
			t.Fatalf("reload: %v", err)
		}
	}

	reload(cronJob("dedupe", "0 4 * * *"))
	if err := e.tick(t, "2026-10-08 03:00:00"); err != nil {
		t.Fatal(err)
	}
	if len(e.inserter.inserted) != 0 {
		t.Fatalf("fired at the old schedule after a reload")
	}
	if err := e.tick(t, "2026-10-08 04:00:00"); err != nil {
		t.Fatal(err)
	}
	if len(e.inserter.inserted) != 1 {
		t.Fatalf("enqueued %d jobs at the new schedule, want 1", len(e.inserter.inserted))
	}

	reload()
	if err := e.tick(t, "2026-10-09 04:00:00"); err != nil {
		t.Fatal(err)
	}
	if len(e.inserter.inserted) != 1 {
		t.Errorf("fired after the cron job was removed: %d jobs in total", len(e.inserter.inserted))
	}
}

func TestTickIdempotencyKeyIsStablePerFireTime(t *testing.T) {
	e := newEnv(t,
		map[string]*module.LoadedModule{"crm": readyModule("crm", cronJob("dedupe", "* * * * *"))},
		fakeEntitlements{"t1": {"crm"}}, "t1",
	)

	for _, at := range []string{"2026-10-08 10:00:00", "2026-10-08 10:00:00", "2026-10-08 10:01:00"} {
		if err := e.tick(t, at); err != nil {
			t.Fatal(err)
		}
	}
	keys := make([]string, len(e.inserter.inserted))
	for i, a := range e.inserter.inserted {
		keys[i] = a.IdempotencyKey
	}
	if keys[0] != keys[1] || keys[1] == keys[2] {
		t.Errorf("idempotency keys = %v, want the same key for a repeated minute and a different one for the next", keys)
	}
}

func TestTickEnqueuesForOtherTenantsWhenOneFails(t *testing.T) {
	e := newEnv(t,
		map[string]*module.LoadedModule{"crm": readyModule("crm", cronJob("dedupe", "* * * * *"))},
		fakeEntitlements{"t1": {"crm"}, "t3": {"crm"}}, // t2's entitlements fail to load
		"t1", "t2", "t3",
	)

	err := e.tick(t, "2026-10-08 10:00:00")

	if err == nil {
		t.Fatal("tick error = nil, want the failed tenant reported so River retries the tick")
	}
	if got, want := e.firedTenants("dedupe"), []string{"t1", "t3"}; !slices.Equal(got, want) {
		t.Errorf("fired for tenants %v, want %v", got, want)
	}
}
