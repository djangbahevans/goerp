package cronsched

import (
	"context"
	"testing"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/auth/membership/membershiptest"
	"github.com/djangbahevans/goerp/internal/engine/cronsettings"
	"github.com/djangbahevans/goerp/internal/engine/jobqueue"
	"github.com/djangbahevans/goerp/internal/engine/manifest"
	"github.com/djangbahevans/goerp/internal/engine/module"
	"github.com/djangbahevans/goerp/internal/engine/tenantschema"
	"github.com/riverqueue/river"
)

func TestTickUsesPersistedChoicesAndSkipsOlderMinutes(t *testing.T) {
	conn := membershiptest.New(t)
	store := cronsettings.NewStore(conn)
	job := cronJob("opt_in", "* * * * *")
	job.EnabledByDefault = new(false)
	e := newEnv(t, map[string]*module.LoadedModule{"crm": readyModule("crm", job)}, fakeEntitlements{"t1": {"crm"}, "t2": {"crm"}}, "t1", "t2")
	for _, slug := range []string{"slug-t1", "slug-t2"} {
		if err := tenantschema.Create(t.Context(), conn, slug); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = tenantschema.Drop(context.Background(), conn, slug) })
		if err := store.Initialize(t.Context(), slug, "crm", []manifest.CronJob{job}); err != nil {
			t.Fatal(err)
		}
	}
	e.worker.Settings = store
	at := time.Now().UTC().Truncate(time.Minute).Add(time.Minute)
	tick := func(minute time.Time) error {
		return e.worker.Work(t.Context(), &river.Job[jobqueue.CronTickArgs]{Args: jobqueue.CronTickArgs{At: minute}})
	}
	if err := tick(at); err != nil {
		t.Fatal(err)
	}
	if len(e.inserter.inserted) != 0 {
		t.Fatal("false default fired before enabling")
	}

	_, err := conn.ExecContext(t.Context(), `UPDATE "tenant_slug-t1".cron_job_settings SET enabled = true, generation = uuidv7(), updated_at = $1`, at.Add(30*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if err := tick(at); err != nil {
		t.Fatal(err)
	}
	if len(e.inserter.inserted) != 0 {
		t.Fatal("retried tick replayed a minute before enablement")
	}
	if err := tick(at.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if len(e.inserter.inserted) != 1 || e.inserter.inserted[0].TenantID != "t1" || e.inserter.inserted[0].CronGeneration == "" {
		t.Fatalf("tenant choices/generation = %+v", e.inserter.inserted)
	}

	if _, err := conn.ExecContext(t.Context(), `DROP TABLE "tenant_slug-t1".cron_job_settings`); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.ExecContext(t.Context(), `UPDATE "tenant_slug-t2".cron_job_settings SET enabled = true`); err != nil {
		t.Fatal(err)
	}
	if err := tick(at.Add(2 * time.Minute)); err == nil {
		t.Fatal("missing settings did not cause tick retry")
	}
	if len(e.inserter.inserted) != 2 || e.inserter.inserted[1].TenantID != "t2" {
		t.Fatalf("failing tenant stopped fan-out: %+v", e.inserter.inserted)
	}
}

func TestTickDoesNotAssumeEnabledWithoutSettingsReader(t *testing.T) {
	e := newEnv(t, map[string]*module.LoadedModule{"crm": readyModule("crm", cronJob("digest", "* * * * *"))}, fakeEntitlements{"t1": {"crm"}}, "t1")
	e.worker.Settings = nil
	if err := e.tick(t, "2026-10-08 10:00:00"); err == nil || len(e.inserter.inserted) != 0 {
		t.Fatalf("unavailable settings failed open: %v", err)
	}
}

func TestIncompleteModuleInitializationBlocksExistingDueJob(t *testing.T) {
	due := cronJob("digest", "* * * * *")
	added := cronJob("new_job", "0 3 * * *")
	e := newEnv(t, map[string]*module.LoadedModule{"crm": readyModule("crm", due, added)}, fakeEntitlements{"t1": {"crm"}}, "t1")
	delete(e.worker.Settings.(fakeSettings)["slug-t1"], cronsettings.Identity{Module: "crm", Name: added.Name})

	if err := e.tick(t, "2026-10-08 10:00:00"); err == nil || len(e.inserter.inserted) != 0 {
		t.Fatalf("incomplete initialization admitted existing job: %v", err)
	}
}
