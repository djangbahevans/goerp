package engine

import (
	"errors"
	"testing"

	abi "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/djangbahevans/goerp/sdk/go/jobs"
	jobdef "github.com/djangbahevans/goerp/sdk/go/jobs/def"
)

func withFreshCronHandlers(t *testing.T) {
	t.Helper()
	origHandlers := cronHandlers
	cronHandlers = map[string]func(*jobs.CronContext) error{}
	t.Cleanup(func() { cronHandlers = origHandlers })
}

func expireQuotations() jobs.CronDef {
	return jobs.DefineCron("sales_expire_quotations", jobs.Schedule("0 3 * * *"), jobs.Label("Expire Quotations"))
}

func TestDispatchCron_InvokesHandlerWithTenantAndTraceID(t *testing.T) {
	withFreshCronHandlers(t)

	var got *jobs.CronContext
	HandleCron(expireQuotations(), func(ctx *jobs.CronContext) error {
		got = ctx
		return nil
	})
	HandleCron(jobs.DefineCron("other_cron", jobs.Schedule("* * * * *"), jobs.Label("Other")), func(*jobs.CronContext) error {
		t.Fatal("other_cron handler ran for sales_expire_quotations")
		return nil
	})

	ptr, length := writeJobEnvelope(t, abi.JobEnvelope{
		JobType: "sales_expire_quotations", TenantID: "tenant_1", TraceID: "trace_1", ModuleName: "sales",
	})

	if status := DispatchCron(ptr, length); status != 0 {
		t.Fatalf("status = %d, want 0", status)
	}
	if got == nil || *got != (jobs.CronContext{TenantID: "tenant_1", TraceID: "trace_1"}) {
		t.Fatalf("CronContext = %+v", got)
	}
}

func TestDispatchCron_ReturnValueStatuses(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want uint32
	}{
		{"nil", nil, 0},
		{"plain error", errors.New("db unavailable"), 1},
		{"permanent", jobs.PermanentError(errors.New("bad config")), 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			withFreshCronHandlers(t)
			HandleCron(expireQuotations(), func(*jobs.CronContext) error { return tt.err })

			ptr, length := writeJobEnvelope(t, abi.JobEnvelope{JobType: "sales_expire_quotations"})
			if status := DispatchCron(ptr, length); status != tt.want {
				t.Errorf("status = %d, want %d", status, tt.want)
			}
		})
	}
}

func TestDispatchCron_UnknownCronAndMalformedEnvelopeFailRetryably(t *testing.T) {
	withFreshCronHandlers(t)

	ptr, length := writeJobEnvelope(t, abi.JobEnvelope{JobType: "unregistered"})
	if status := DispatchCron(ptr, length); status != 1 {
		t.Errorf("unknown cron status = %d, want 1", status)
	}

	bad := Allocate(3)
	WriteMem(bad, []byte{0xc1, 0xc1, 0xc1})
	if status := DispatchCron(bad, 3); status != 1 {
		t.Errorf("malformed envelope status = %d, want 1", status)
	}
}

func TestHandleCron_RejectsDuplicateAndNilHandler(t *testing.T) {
	withFreshCronHandlers(t)
	def := expireQuotations()
	HandleCron(def, func(*jobs.CronContext) error { return nil })

	mustPanic(t, "duplicate registration", func() { HandleCron(def, func(*jobs.CronContext) error { return nil }) })
	mustPanic(t, "nil handler", func() {
		HandleCron(jobs.DefineCron("other_cron", jobs.Schedule("* * * * *"), jobs.Label("Other")), nil)
	})
}

func namedCronHandler(*jobs.CronContext) error { return nil }

func TestHandleCron_RecordsTheHandlerRoutingName(t *testing.T) {
	withFreshCronHandlers(t)
	HandleCron(jobs.DefineCron("cron_named", jobs.Schedule("* * * * *"), jobs.Label("Named")), namedCronHandler)
	HandleCron(jobs.DefineCron("cron_closure", jobs.Schedule("* * * * *"), jobs.Label("Closure")), func(*jobs.CronContext) error { return nil })

	if got := lastHandler(t, jobdef.KindCronHandler, "cron_named"); got != "namedCronHandler" {
		t.Errorf("named handler routing name = %q, want namedCronHandler", got)
	}
	if got := lastHandler(t, jobdef.KindCronHandler, "cron_closure"); got != "cron_closure" {
		t.Errorf("closure handler routing name = %q, want the cron job's own name", got)
	}
}

func mustPanic(t *testing.T, name string, fn func()) {
	t.Helper()
	defer func() {
		if recover() == nil {
			t.Errorf("%s did not panic", name)
		}
	}()
	fn()
}
