package eventdelivery

import (
	"errors"
	"strings"
	"testing"

	"github.com/djangbahevans/goerp/internal/engine/jobqueue"
	"github.com/djangbahevans/goerp/internal/engine/manifest"
	"github.com/djangbahevans/goerp/internal/engine/module"
	"github.com/djangbahevans/goerp/sdk/go/model"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
)

func TestSubscriberWork_QueuedVersionCompatibility(t *testing.T) {
	for _, tt := range []struct {
		name     string
		pin      string
		boundary string
		cancel   bool
	}{
		{name: "untagged", boundary: ">= 2.0.0"},
		{name: "same version skips migrations", pin: "2.0.0", boundary: "invalid"},
		{name: "additive upgrade", pin: "1.0.0"},
		{name: "migration before pin", pin: "1.0.0", boundary: ">= 1.0.0"},
		{name: "migration after live version", pin: "1.0.0", boundary: ">= 3.0.0"},
		{name: "incompatible upgrade", pin: "1.0.0", boundary: ">= 2.0.0", cancel: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			wasmBytes := buildHandleEventConstStatusModule(0)
			if tt.cancel {
				wasmBytes = handleEventTrapsModule
			}
			w := newTestSubscriberWorker(t, wasmBytes)
			mod := *w.ModuleRegistry.Snapshot().Modules()[testEventModuleName]
			mod.Manifest.Version = "2.0.0"
			if tt.boundary != "" {
				mod.DataMigrations = []model.DataMigration{{ToVersion: tt.boundary, Handler: "migrate"}}
			}
			if _, err := w.ModuleRegistry.Update(map[string]*module.LoadedModule{testEventModuleName: &mod}); err != nil {
				t.Fatal(err)
			}

			var tags []string
			if tt.pin != "" {
				tags = []string{jobqueue.ModuleVersionTag(tt.pin)}
			}
			err := w.Work(t.Context(), &river.Job[jobqueue.SubscriberDeliveryArgs]{
				JobRow: new(rivertype.JobRow{Tags: tags}),
				Args: jobqueue.SubscriberDeliveryArgs{
					EventName: testEventName, EventVersion: 1,
					ModuleName: testEventModuleName, HandlerName: testHandlerName,
				},
			})
			if tt.cancel {
				if _, cancelled := errors.AsType[*river.JobCancelError](err); !cancelled {
					t.Fatalf("Work() = %v, want permanent cancellation before the trapping handler", err)
				}
				if !strings.Contains(err.Error(), "1.0.0") || !strings.Contains(err.Error(), "2.0.0") {
					t.Fatalf("cancellation omits module versions: %v", err)
				}
			} else if err != nil {
				t.Fatalf("compatible event failed: %v", err)
			}
		})
	}
}

func TestWork_FanOutPinsSubscriberVersionAcrossReloads(t *testing.T) {
	w, tenantStore, conn, ctx := newTestWorker(t, testEventName, []manifest.EventSubscription{
		{Name: testEventName, Handler: testHandlerName, Async: true},
	})
	tn := newTestTenant(t, tenantStore, conn, uniqueSlug(t))
	mod := *w.ModuleRegistry.Snapshot().Modules()[testEventModuleName]
	mod.Manifest.Version = "1.2.0"
	if _, err := w.ModuleRegistry.Update(map[string]*module.LoadedModule{testEventModuleName: &mod}); err != nil {
		t.Fatal(err)
	}

	args := jobqueue.EventDeliveryArgs{
		EventID:       uniqueEventID(t, conn),
		EventName:     testEventName,
		EventVersion:  1,
		EmitterModule: "sales",
		TenantID:      tn.ID,
		Payload:       mustMarshal(t, map[string]any{}),
	}
	job := &river.Job[jobqueue.EventDeliveryArgs]{JobRow: new(rivertype.JobRow{}), Args: args}
	if err := w.Work(ctx, job); err != nil {
		t.Fatal(err)
	}

	reloaded := mod
	reloaded.Manifest.Version = "2.0.0"
	if _, err := w.ModuleRegistry.Update(map[string]*module.LoadedModule{testEventModuleName: &reloaded}); err != nil {
		t.Fatal(err)
	}
	if err := w.Work(ctx, job); err != nil {
		t.Fatal(err)
	}

	var tag string
	var count int
	if err := conn.QueryRow(`SELECT tags[1], count(*) OVER () FROM system.river_job WHERE kind = 'subscriber_delivery' AND args->>'event_id' = $1`, args.EventID).Scan(&tag, &count); err != nil {
		t.Fatal(err)
	}
	if tag != "module_version-312e322e30" || count != 1 {
		t.Fatalf("fan-out tag = %q, jobs = %d; want original subscriber version and one job", tag, count)
	}

	job.Args.EventID = uniqueEventID(t, conn)
	if err := w.Work(ctx, job); err != nil {
		t.Fatal(err)
	}
	if err := conn.QueryRow(`SELECT tags[1] FROM system.river_job WHERE kind = 'subscriber_delivery' AND args->>'event_id' = $1`, job.Args.EventID).Scan(&tag); err != nil {
		t.Fatal(err)
	}
	if tag != "module_version-322e302e30" {
		t.Fatalf("new fan-out tag = %q, want current subscriber version", tag)
	}
}
