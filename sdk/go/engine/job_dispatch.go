package engine

import (
	abi "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/djangbahevans/goerp/sdk/go/jobs"
	"github.com/djangbahevans/goerp/sdk/go/model"
)

// DispatchJob is what a module's handle_job export calls
// (manifest-spec.md §26): decode the incoming abi.JobEnvelope, route it to
// the handler registered via OnJob for its job type, or via
// OnDataMigration when the envelope marks a data migration job, and
// return the i32 status handle_job's ABI reserves — the same codes as
// DispatchEvent: 0 success, 1 retryable failure, 2 permanent failure
// (jobs.PermanentError). jobs.RetryAfter's delay isn't carried over this
// ABI, so it degrades to status 1 and the job retries on its ordinary
// backoff.
func DispatchJob(ptr, length uint32) uint32 {
	buf := ReadMem(ptr, length)

	var env abi.JobEnvelope
	if err := unmarshal(buf, &env); err != nil {
		return 1
	}

	if env.IsDataMigration {
		return dispatchDataMigration(env.Payload)
	}

	handler, ok := jobHandlers[env.JobType]
	if !ok {
		return 1
	}

	ctx := &JobContext{
		JobID:       env.JobID,
		JobType:     env.JobType,
		TenantID:    env.TenantID,
		TraceID:     env.TraceID,
		Attempt:     env.Attempt,
		MaxAttempts: env.MaxAttempts,
	}
	return jobStatus(handler(ctx, env.Payload))
}

// dispatchDataMigration runs the handler registered via OnDataMigration
// that payload, a msgpack model.MigrationJobPayload, names.
func dispatchDataMigration(payload []byte) uint32 {
	var p model.MigrationJobPayload
	if err := unmarshal(payload, &p); err != nil {
		return 1
	}

	handler, ok := migrationHandlers[p.Handler]
	if !ok {
		return 1
	}

	ctx := model.NewMigrationContext(p)
	err := handler(ctx)
	if err != nil {
		ctx.Log("handler failed", "error", err.Error())
	}
	return jobStatus(err)
}

// jobStatus maps a job handler's returned error onto handle_job's status
// codes.
func jobStatus(err error) uint32 {
	if err == nil {
		return 0
	}
	if jobs.IsPermanentError(err) {
		return 2
	}
	return 1
}
