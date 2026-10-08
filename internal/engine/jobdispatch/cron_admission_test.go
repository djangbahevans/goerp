package jobdispatch

import (
	"context"
	"errors"
	"maps"
	"testing"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/cronsettings"
	"github.com/djangbahevans/goerp/internal/engine/jobqueue"
	"github.com/djangbahevans/goerp/internal/engine/manifest"
	"github.com/djangbahevans/goerp/internal/engine/module"
	"github.com/djangbahevans/goerp/internal/engine/tenantschema"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
)

func (e *jobFixtureEnv) cronArgs(t *testing.T, name string) jobqueue.WASMJobArgs {
	t.Helper()
	id := cronsettings.Identity{Module: jobFixtureModuleName, Name: name}
	states, err := e.settings.Read(t.Context(), e.tenantSlug, []cronsettings.Identity{id})
	if err != nil {
		t.Fatal(err)
	}
	return jobqueue.WASMJobArgs{
		ModuleName:     id.Module,
		JobType:        id.Name,
		TenantID:       e.tenantID,
		IsCron:         true,
		CronGeneration: states[id].Generation,
		MaxAttempts:    3,
	}
}

func (e *jobFixtureEnv) writeCronState(t *testing.T, name string, enabled bool) {
	t.Helper()
	_, err := e.conn.ExecContext(t.Context(), "UPDATE "+tenantschema.Name(e.tenantSlug)+
		`.cron_job_settings SET enabled = $1, generation = uuidv7(), updated_at = clock_timestamp() WHERE cron_name = $2`, enabled, name)
	if err != nil {
		t.Fatal(err)
	}
}

func TestCronPendingAndRetryRejectOldGeneration(t *testing.T) {
	e := newJobFixtureEnv(t)
	args := e.cronArgs(t, "jobfixture_cron")
	e.writeCronState(t, args.JobType, false)
	e.writeCronState(t, args.JobType, true)
	res, err := e.tester.Work(t.Context(), t, e.tx, args, nil)
	if err != nil || res.Job.State != rivertype.JobStateCancelled || len(e.observations(t)) != 0 {
		t.Fatalf("old pending run state=%s err=%v", res.Job.State, err)
	}

	res, err = e.insertAndWorkCron(t, "jobfixture_cron_fail", 3, "")
	if err == nil {
		t.Fatal("failing handler did not fail its admitted attempt")
	}
	e.writeCronState(t, "jobfixture_cron_fail", false)
	e.writeCronState(t, "jobfixture_cron_fail", true)
	res, err = e.work(t, res.Job)
	if err != nil || res.Job.State != rivertype.JobStateCancelled {
		t.Fatalf("old retry state=%s err=%v", res.Job.State, err)
	}
}

func TestCronFinalAdmissionAfterWaitingForInstanceCleansRejectedContext(t *testing.T) {
	e := newJobFixtureEnv(t)
	args := e.cronArgs(t, "jobfixture_cron")
	pool := e.worker.ModuleRegistry.Snapshot().Modules()[jobFixtureModuleName].Pool
	first, err := pool.Borrow(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	second, err := pool.Borrow(t.Context())
	if err != nil {
		pool.Return(first)
		t.Fatal(err)
	}

	result := make(chan error, 1)
	go func() {
		result <- e.worker.Work(t.Context(), &river.Job[jobqueue.WASMJobArgs]{JobRow: &rivertype.JobRow{ID: 987, Attempt: 1, MaxAttempts: 3}, Args: args})
	}()
	e.writeCronState(t, args.JobType, false)
	pool.Return(first)
	pool.Return(second)
	if err := <-result; !errors.Is(err, cronsettings.ErrObsolete) {
		t.Fatalf("attempt waiting for instance passed disabled gate: %v", err)
	}
	if len(e.observations(t)) != 0 {
		t.Fatal("rejected run invoked its handler")
	}

	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	for range 3 {
		inst, err := pool.Borrow(ctx)
		if err != nil {
			t.Fatalf("rejection leaked borrowed instance: %v", err)
		}
		if inst.ModuleContext() != nil {
			t.Error("rejection retained module context")
		}
		pool.Return(inst)
	}
}

func TestCronAdmissionTerminalAndTransientEligibility(t *testing.T) {
	for _, scenario := range []string{"inactive", "disabled_module", "unentitled", "removed_job", "uninstalled", "not_ready", "missing_settings", "incomplete_initialization", "unparsed_unavailable"} {
		t.Run(scenario, func(t *testing.T) {
			e := newJobFixtureEnv(t)
			args := e.cronArgs(t, "jobfixture_cron")
			reg := e.worker.ModuleRegistry
			modules := maps.Clone(reg.Snapshot().Modules())
			m := *modules[jobFixtureModuleName]
			switch scenario {
			case "inactive":
				if _, err := e.conn.ExecContext(t.Context(), `UPDATE system.tenants SET status = 'suspended' WHERE id = $1`, e.tenantID); err != nil {
					t.Fatal(err)
				}
			case "disabled_module":
				if _, err := e.conn.ExecContext(t.Context(), `INSERT INTO system.tenant_module_settings (tenant_id, module_name, enabled) VALUES ($1, $2, false)`, e.tenantID, jobFixtureModuleName); err != nil {
					t.Fatal(err)
				}
			case "unentitled":
				if _, err := e.conn.ExecContext(t.Context(), `UPDATE system.tenant_entitlement_overrides SET value = 'false' WHERE tenant_id = $1`, e.tenantID); err != nil {
					t.Fatal(err)
				}
			case "removed_job":
				m.Manifest.CronJobs = []manifest.CronJob{}
				modules[jobFixtureModuleName] = &m
			case "uninstalled":
				delete(modules, jobFixtureModuleName)
			case "unparsed_unavailable":
				m.Status = module.StatusFailed
				m.Manifest = manifest.Manifest{}
				modules[jobFixtureModuleName] = &m
			case "not_ready":
				m.Status = module.StatusFailed
				modules[jobFixtureModuleName] = &m
			case "incomplete_initialization":
				m.Manifest.CronJobs = append(m.Manifest.CronJobs, manifest.CronJob{Name: "uninitialized", Label: "Uninitialized", Handler: "uninitialized", Schedule: "0 3 * * *"})
				modules[jobFixtureModuleName] = &m
			case "missing_settings":
				if _, err := e.conn.ExecContext(t.Context(), "DELETE FROM "+tenantschema.Name(e.tenantSlug)+".cron_job_settings"); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := reg.Update(modules); err != nil {
				t.Fatal(err)
			}

			err := e.worker.Work(t.Context(), &river.Job[jobqueue.WASMJobArgs]{JobRow: &rivertype.JobRow{ID: 988, Attempt: 1, MaxAttempts: 3}, Args: args})
			_, cancelled := errors.AsType[*river.JobCancelError](err)
			wantCancelled := scenario != "not_ready" && scenario != "missing_settings" && scenario != "incomplete_initialization" && scenario != "unparsed_unavailable"
			if err == nil || cancelled != wantCancelled || len(e.observations(t)) != 0 {
				t.Fatalf("eligibility=%s cancelled=%v want=%v err=%v", scenario, cancelled, wantCancelled, err)
			}

			admissionErr := e.worker.admitCron(t.Context(), args)
			_, admissionCancelled := errors.AsType[*river.JobCancelError](admissionErr)
			if admissionErr == nil || admissionCancelled != wantCancelled {
				t.Fatalf("final eligibility=%s cancelled=%v want=%v err=%v", scenario, admissionCancelled, wantCancelled, admissionErr)
			}
		})
	}
}

func TestAdmittedCronFinishesAfterDisableWithoutHoldingSettingsLock(t *testing.T) {
	e := newJobFixtureEnv(t)
	args := e.cronArgs(t, "jobfixture_cron_wait")
	result := make(chan error, 1)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	go func() {
		result <- e.worker.Work(ctx, &river.Job[jobqueue.WASMJobArgs]{
			JobRow: &rivertype.JobRow{ID: 989, Attempt: 1, MaxAttempts: 3},
			Args:   args,
		})
	}()

	for len(e.observations(t)) == 0 {
		select {
		case err := <-result:
			t.Fatalf("handler returned before reporting admission: %v", err)
		case <-ctx.Done():
			t.Fatal("handler did not start")
		case <-time.After(10 * time.Millisecond):
		}
	}

	toggleCtx, toggleCancel := context.WithTimeout(ctx, time.Second)
	defer toggleCancel()
	_, err := e.conn.ExecContext(toggleCtx, "UPDATE "+tenantschema.Name(e.tenantSlug)+
		`.cron_job_settings SET enabled = false, generation = uuidv7() WHERE cron_name = $1`, args.JobType)
	if err != nil {
		t.Fatalf("running handler held settings lock: %v", err)
	}
	if err := <-result; err != nil {
		t.Fatalf("admitted handler failed after disable: %v", err)
	}

	observations := e.observations(t)
	if len(observations) != 2 || observations[0].Note != "started" || observations[1].Note != "finished" {
		t.Fatalf("admitted handler observations = %+v", observations)
	}
}
