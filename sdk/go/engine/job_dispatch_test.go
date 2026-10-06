package engine

import (
	"encoding/json/v2"
	"errors"
	"fmt"
	"testing"
	"time"

	abi "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/djangbahevans/goerp/sdk/go/declare"
	"github.com/djangbahevans/goerp/sdk/go/jobs"
	jobdef "github.com/djangbahevans/goerp/sdk/go/jobs/def"
	"github.com/djangbahevans/goerp/sdk/go/model"
)

func withFreshJobHandlers(t *testing.T) {
	t.Helper()
	origJobs, origMigrations := jobHandlers, migrationHandlers
	jobHandlers = map[string]jobHandler{}
	migrationHandlers = map[string]migrationHandler{}
	t.Cleanup(func() { jobHandlers, migrationHandlers = origJobs, origMigrations })
}

// declaredHandlers returns the handler declarations of kind recorded for
// name in sdk/go/declare, which no test resets.
func declaredHandlers(t *testing.T, kind, name string) []jobdef.HandlerDeclaration {
	t.Helper()
	data, err := declare.Export()
	if err != nil {
		t.Fatalf("declare.Export: %v", err)
	}
	var byKind map[string][]jobdef.HandlerDeclaration
	if err := json.Unmarshal(data, &byKind); err != nil {
		t.Fatalf("decode declarations: %v", err)
	}
	var out []jobdef.HandlerDeclaration
	for _, d := range byKind[kind] {
		if d.Name == name {
			out = append(out, d)
		}
	}
	return out
}

func lastHandler(t *testing.T, kind, name string) string {
	t.Helper()
	decls := declaredHandlers(t, kind, name)
	if len(decls) == 0 {
		t.Fatalf("no %s declaration for %q", kind, name)
	}
	return decls[len(decls)-1].Handler
}

func sendDef(name string) jobs.Def[sendPayload] {
	return jobs.Define[sendPayload](name, jobs.Label(name))
}

func writeJobEnvelope(t *testing.T, env abi.JobEnvelope) (ptr, length uint32) {
	t.Helper()
	data, err := marshal(env)
	if err != nil {
		t.Fatalf("marshal job envelope: %v", err)
	}
	ptr = Allocate(uint32(len(data)))
	WriteMem(ptr, data)
	return ptr, uint32(len(data))
}

func mustMarshal(t *testing.T, v any) []byte {
	t.Helper()
	data, err := marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return data
}

type sendPayload struct {
	To   string `msgpack:"to"`
	Body string `msgpack:"body"`
}

func TestDispatchJob_RoutesByJobTypeWithTypedPayloadAndContext(t *testing.T) {
	withFreshJobHandlers(t)

	var gotCtx *JobContext
	var gotPayload sendPayload
	HandleJob(sendDef("sms_send"), func(ctx *JobContext, p sendPayload) error {
		gotCtx, gotPayload = ctx, p
		return nil
	})
	HandleJob(sendDef("push_send"), func(ctx *JobContext, p sendPayload) error {
		t.Fatal("push_send handler ran for an sms_send job")
		return nil
	})

	ptr, length := writeJobEnvelope(t, abi.JobEnvelope{
		JobID: "job_42", JobType: "sms_send", TenantID: "tenant_1", ModuleName: "notify",
		UserID: "user_1", TraceID: "trace_1", Attempt: 2, MaxAttempts: 5,
		Payload: mustMarshal(t, sendPayload{To: "+233200000000", Body: "hi"}),
	})

	if status := DispatchJob(ptr, length); status != 0 {
		t.Fatalf("status = %d, want 0", status)
	}
	want := JobContext{JobID: "job_42", JobType: "sms_send", TenantID: "tenant_1", UserID: "user_1", TraceID: "trace_1", Attempt: 2, MaxAttempts: 5}
	if gotCtx == nil || *gotCtx != want {
		t.Fatalf("JobContext = %+v, want %+v", gotCtx, want)
	}
	if gotPayload != (sendPayload{To: "+233200000000", Body: "hi"}) {
		t.Fatalf("payload = %+v", gotPayload)
	}
}

func TestDispatchJob_EmptyPayloadLeavesZeroValue(t *testing.T) {
	withFreshJobHandlers(t)

	called := false
	HandleJob(sendDef("sweep"), func(_ *JobContext, p sendPayload) error {
		called = true
		if p != (sendPayload{}) {
			t.Errorf("payload = %+v, want zero value", p)
		}
		return nil
	})

	ptr, length := writeJobEnvelope(t, abi.JobEnvelope{JobType: "sweep"})
	if status := DispatchJob(ptr, length); status != 0 {
		t.Fatalf("status = %d, want 0", status)
	}
	if !called {
		t.Fatal("handler was never invoked")
	}
}

func TestDispatchJob_ReturnValueStatuses(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want uint32
	}{
		{"nil", nil, 0},
		{"plain error", errors.New("smtp timeout"), 1},
		{"permanent", jobs.PermanentError(errors.New("invalid credentials")), 2},
		{"wrapped permanent", fmt.Errorf("send: %w", jobs.PermanentError(errors.New("403"))), 2},
		{"retry after degrades to retryable", jobs.RetryAfter(time.Minute, errors.New("rate limited")), 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			withFreshJobHandlers(t)
			HandleJob(jobs.Define[any]("job", jobs.Label("Job")), func(*JobContext, any) error { return tt.err })

			ptr, length := writeJobEnvelope(t, abi.JobEnvelope{JobType: "job"})
			if got := DispatchJob(ptr, length); got != tt.want {
				t.Fatalf("status = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestDispatchJob_UndecodablePayloadIsPermanent(t *testing.T) {
	withFreshJobHandlers(t)
	HandleJob(sendDef("sms_send"), func(*JobContext, sendPayload) error {
		t.Fatal("handler ran for an undecodable payload")
		return nil
	})

	ptr, length := writeJobEnvelope(t, abi.JobEnvelope{JobType: "sms_send", Payload: mustMarshal(t, []int{1, 2})})
	if status := DispatchJob(ptr, length); status != 2 {
		t.Fatalf("status = %d, want 2", status)
	}
}

func TestHandleJob_DuplicateDefinitionPanics(t *testing.T) {
	withFreshJobHandlers(t)
	noop := func(*JobContext, sendPayload) error { return nil }
	HandleJob(sendDef("sms_send"), noop)

	before := len(declaredHandlers(t, jobdef.KindJobHandler, "sms_send"))
	mustPanic(t, "second registration of sms_send", func() { HandleJob(sendDef("sms_send"), noop) })
	if got := len(declaredHandlers(t, jobdef.KindJobHandler, "sms_send")); got != before {
		t.Errorf("a rejected registration recorded a declaration: %d, want %d", got, before)
	}
}

func TestHandleJob_NilHandlerPanics(t *testing.T) {
	withFreshJobHandlers(t)
	mustPanic(t, "nil handler", func() { HandleJob[sendPayload](sendDef("sms_send"), nil) })
}

func TestHandleJob_ZeroValueDefinitionPanics(t *testing.T) {
	withFreshJobHandlers(t)
	mustPanic(t, "zero-value definition", func() { HandleJob(jobs.Def[sendPayload]{}, namedJobHandler) })
}

func TestHandleProviderJob_RoutesAndDecodesLikeHandleJobButRecordsNoRegistration(t *testing.T) {
	withFreshJobHandlers(t)

	before := len(declaredHandlers(t, jobdef.KindJobHandler, "sms_send"))
	var got sendPayload
	HandleProviderJob(jobs.DefineProvider[sendPayload, struct{}]("sms_provider", "sms_send"), func(_ *JobContext, p sendPayload) error {
		got = p
		return nil
	})

	ptr, length := writeJobEnvelope(t, abi.JobEnvelope{JobType: "sms_send", Payload: mustMarshal(t, sendPayload{To: "+233200000000"})})
	if status := DispatchJob(ptr, length); status != 0 || got.To != "+233200000000" {
		t.Fatalf("status = %d, payload = %+v", status, got)
	}
	if n := len(declaredHandlers(t, jobdef.KindJobHandler, "sms_send")); n != before {
		t.Errorf("a provider job recorded %d handler declarations, want %d: it generates no job_types entry", n, before)
	}

	ptr, length = writeJobEnvelope(t, abi.JobEnvelope{JobType: "sms_send", Payload: mustMarshal(t, []int{1})})
	if status := DispatchJob(ptr, length); status != 2 {
		t.Errorf("undecodable payload status = %d, want 2", status)
	}
}

func TestHandleProviderJob_RejectsDuplicateNilAndZeroValue(t *testing.T) {
	withFreshJobHandlers(t)
	def := jobs.DefineProvider[sendPayload, struct{}]("sms_provider", "sms_send")
	HandleProviderJob(def, namedJobHandler)

	mustPanic(t, "duplicate", func() { HandleProviderJob(def, namedJobHandler) })
	mustPanic(t, "same name as a HandleJob registration", func() { HandleJob(sendDef("sms_send"), namedJobHandler) })
	mustPanic(t, "nil handler", func() {
		HandleProviderJob[sendPayload, struct{}](jobs.DefineProvider[sendPayload, struct{}]("sms_provider", "push_send"), nil)
	})
	mustPanic(t, "zero value", func() { HandleProviderJob(jobs.ProviderDef[sendPayload, struct{}]{}, namedJobHandler) })
}

func namedJobHandler(*JobContext, sendPayload) error { return nil }

func TestHandleJob_RecordsTheHandlerRoutingName(t *testing.T) {
	withFreshJobHandlers(t)
	HandleJob(sendDef("routing_named"), namedJobHandler)
	HandleJob(sendDef("routing_closure"), func(*JobContext, sendPayload) error { return nil })

	if got := lastHandler(t, jobdef.KindJobHandler, "routing_named"); got != "namedJobHandler" {
		t.Errorf("named handler routing name = %q, want namedJobHandler", got)
	}
	if got := lastHandler(t, jobdef.KindJobHandler, "routing_closure"); got != "routing_closure" {
		t.Errorf("closure handler routing name = %q, want the job's own name, which does not change with the closure's position", got)
	}
}

func TestDispatchJob_UnregisteredJobTypeIsRetryable(t *testing.T) {
	withFreshJobHandlers(t)

	ptr, length := writeJobEnvelope(t, abi.JobEnvelope{JobType: "no_such_job"})
	if status := DispatchJob(ptr, length); status != 1 {
		t.Fatalf("status = %d, want 1", status)
	}
}

func TestDispatchJob_MalformedEnvelopeIsRetryable(t *testing.T) {
	bad := []byte("not msgpack")
	ptr := Allocate(uint32(len(bad)))
	WriteMem(ptr, bad)

	if status := DispatchJob(ptr, uint32(len(bad))); status != 1 {
		t.Fatalf("status = %d, want 1", status)
	}
}

func writeMigrationJob(t *testing.T, payload any) (ptr, length uint32) {
	t.Helper()
	return writeJobEnvelope(t, abi.JobEnvelope{
		JobType: "backfill_test", TenantID: "tenant-1", IsDataMigration: true,
		Payload: mustMarshal(t, payload),
	})
}

func TestDispatchJob_DataMigrationInvokesRegisteredHandlerWithDecodedContext(t *testing.T) {
	withFreshJobHandlers(t)

	var got *model.MigrationContext
	OnDataMigration("backfill_test", func(ctx *model.MigrationContext) error {
		got = ctx
		return nil
	})
	// A HandleJob handler of the same name must not be picked for a data
	// migration job: the two name spaces are separate.
	HandleJob(jobs.Define[any]("backfill_test", jobs.Label("Backfill")), func(*JobContext, any) error {
		t.Fatal("HandleJob handler ran for a data migration job")
		return nil
	})

	ptr, length := writeMigrationJob(t, model.MigrationJobPayload{
		Handler: "backfill_test", TenantID: "tenant-1", FromVersion: "1.3.0", ToVersion: "1.4.0",
	})
	if status := DispatchJob(ptr, length); status != 0 {
		t.Fatalf("status = %d, want 0", status)
	}
	if got == nil {
		t.Fatal("handler was never invoked")
	}
	if got.TenantID != "tenant-1" || got.FromVersion != "1.3.0" || got.ToVersion != "1.4.0" {
		t.Errorf("MigrationContext = %+v, want TenantID=tenant-1 FromVersion=1.3.0 ToVersion=1.4.0", got)
	}
}

func TestDispatchJob_DataMigrationFailureStatuses(t *testing.T) {
	withFreshJobHandlers(t)
	OnDataMigration("failing_test", func(*model.MigrationContext) error { return errors.New("boom") })
	OnDataMigration("permanent_test", func(*model.MigrationContext) error {
		return jobs.PermanentError(errors.New("unrecoverable"))
	})

	tests := []struct {
		name    string
		payload any
		want    uint32
	}{
		{"handler error", model.MigrationJobPayload{Handler: "failing_test"}, 1},
		{"permanent handler error", model.MigrationJobPayload{Handler: "permanent_test"}, 2},
		{"unregistered handler", model.MigrationJobPayload{Handler: "no_such_handler"}, 1},
		{"undecodable payload", []int{1, 2}, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ptr, length := writeMigrationJob(t, tt.payload)
			if got := DispatchJob(ptr, length); got != tt.want {
				t.Fatalf("status = %d, want %d", got, tt.want)
			}
		})
	}
}
