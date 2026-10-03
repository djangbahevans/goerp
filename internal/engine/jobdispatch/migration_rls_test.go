package jobdispatch

import (
	"context"
	"strings"
	"testing"

	"github.com/djangbahevans/goerp/internal/engine/db"
	"github.com/djangbahevans/goerp/internal/engine/jobqueue"
	"github.com/djangbahevans/goerp/internal/engine/manifest"
	"github.com/djangbahevans/goerp/internal/engine/module"
	"github.com/djangbahevans/goerp/internal/engine/recordactivity"
	"github.com/djangbahevans/goerp/sdk/go/model"
	"github.com/riverqueue/river/rivertest"
	"github.com/vmihailenco/msgpack/v5"
)

func TestWork_DataMigrationBypassesRLSAndPreservesSideEffects(t *testing.T) {
	f := newJobIdentityFixture(t)
	ctx := t.Context()
	f.setPolicy(t, "false")

	if _, err := f.admin.ExecContext(ctx, `CREATE TABLE `+f.schema+`.audit_log (
		id UUID DEFAULT uuidv7(), table_name TEXT, record_id UUID,
		operation TEXT, old_data JSONB, new_data JSONB, changed_by UUID,
		changed_at TIMESTAMPTZ DEFAULT now(), request_id TEXT, trace_id TEXT
	)`); err != nil {
		t.Fatal(err)
	}

	if err := recordactivity.NewStore(f.admin).Bootstrap(ctx, f.tenant.Slug); err != nil {
		t.Fatal(err)
	}

	if _, err := f.admin.ExecContext(ctx, `
		GRANT USAGE ON SCHEMA `+f.schema+` TO schema_sync_user;
		GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA `+f.schema+` TO schema_sync_user;
		GRANT USAGE ON SCHEMA system TO schema_sync_user;
		GRANT SELECT, INSERT, UPDATE ON system.river_job TO schema_sync_user;
		GRANT USAGE ON SEQUENCE system.river_job_id_seq TO schema_sync_user
	`); err != nil {
		t.Fatal(err)
	}

	syncDB, err := db.New("postgres://schema_sync_user:dev@localhost:15432/goerp")
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = syncDB.Close() })
	f.worker.Runtime.SetSchemaSyncDB(syncDB)
	_, f.worker.SchemaSyncPool = openTestSchemaSyncPool(t)

	handlers := []string{"migration_sql", "migration_tx", "migration_batch", "migration_orm", "migration_orm_tx"}
	mod := *f.worker.ModuleRegistry.Snapshot().Modules()["identityfixture"]
	mod.Manifest.Version = "1.0.0"
	mod.Manifest.AuditedTables = []manifest.AuditedTable{{Table: "widget"}}
	for i := range mod.ModelDecls[0].Fields {
		field := &mod.ModelDecls[0].Fields[i]
		if field.Name == "seen" {
			field.Def = field.Def.Tracked()
		}
	}

	for _, handler := range handlers {
		mod.DataMigrations = append(mod.DataMigrations, model.DataMigration{FromVersion: "< 1.0.0", ToVersion: ">= 1.0.0", Handler: handler})
	}

	if _, err := f.worker.ModuleRegistry.Update(map[string]*module.LoadedModule{"identityfixture": &mod}); err != nil {
		t.Fatal(err)
	}

	seedSyncedRow(t, f.worker.SchemaSyncPool, f.tenant.ID, "identityfixture", "1.0.0")
	t.Cleanup(func() {
		_, _ = f.admin.ExecContext(context.WithoutCancel(ctx), "DELETE FROM system.module_schema_versions WHERE tenant_id = $1", f.tenant.ID)
	})

	// The same runtime must keep an ordinary job on its non-bypass pool.
	f.read(t, "ordinary", "", "", "", "")

	riverClient := newTestRiverClient(t)
	for _, handler := range handlers {
		t.Run(handler, func(t *testing.T) {
			ctx := t.Context()
			if _, err := f.admin.ExecContext(ctx, "UPDATE "+f.schema+".widget SET seen = 0; TRUNCATE "+f.schema+".audit_log, "+f.schema+".record_activity"); err != nil {
				t.Fatal(err)
			}

			if _, err := f.admin.ExecContext(ctx, "DELETE FROM system.river_job WHERE args->>'tenant_id' = $1", f.tenant.ID); err != nil {
				t.Fatal(err)
			}

			payload, err := msgpack.Marshal(model.MigrationJobPayload{Handler: handler, TenantID: f.tenant.ID, FromVersion: "0.0.0", ToVersion: "1.0.0"})
			if err != nil {
				t.Fatal(err)
			}

			args := jobqueue.WASMJobArgs{
				ModuleName: "identityfixture", TenantID: f.tenant.ID, JobType: handler,
				IsDataMigration: true, MigrationToVersion: "1.0.0", Payload: payload,
			}

			if err := f.worker.Work(rivertest.WorkContext(ctx, riverClient), testJob(args)); err != nil {
				t.Fatal(err)
			}

			var updated, audited, activities, events int
			checks := []struct {
				query string
				dest  *int
			}{
				{"SELECT count(*) FROM " + f.schema + ".widget WHERE seen = 1", &updated},
				{"SELECT count(*) FROM " + f.schema + ".audit_log WHERE operation = 'UPDATE' AND changed_by IS NULL AND (old_data->>'seen')::int = 0 AND (new_data->>'seen')::int = 1", &audited},
				{"SELECT count(*) FROM " + f.schema + ".record_activity WHERE kind = 'change'", &activities},
			}

			for _, check := range checks {
				if err := f.admin.QueryRowContext(ctx, check.query).Scan(check.dest); err != nil {
					t.Fatal(err)
				}
			}

			if err := f.admin.QueryRowContext(ctx, "SELECT count(*) FROM system.river_job WHERE args->>'tenant_id' = $1 AND args->>'event_name' = 'orm.record.updated'", f.tenant.ID).Scan(&events); err != nil {
				t.Fatal(err)
			}

			if updated != 2 || audited != 2 {
				t.Errorf("updated = %d, audited = %d; want 2 of each", updated, audited)
			}

			wantActivities, wantEvents := 2, 0
			if strings.HasPrefix(handler, "migration_orm") {
				wantEvents = 2
			}

			if activities != wantActivities || events != wantEvents {
				t.Errorf("activities = %d, events = %d; want %d, %d", activities, events, wantActivities, wantEvents)
			}
		})
	}

	f.read(t, "ordinary", "", "", "", "")
}
