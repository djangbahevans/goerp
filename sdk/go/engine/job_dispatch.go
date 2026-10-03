package engine

import (
	abi "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/djangbahevans/goerp/sdk/go/jobs"
	"github.com/djangbahevans/goerp/sdk/go/model"
)

// RetryAfter's delay is not carried by the status-only handle_job ABI; it uses
// ordinary retry backoff. Permanent errors return status 2 instead of 1.
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
		UserID:      env.UserID,
		TraceID:     env.TraceID,
		Attempt:     env.Attempt,
		MaxAttempts: env.MaxAttempts,
	}
	return jobStatus(handler(ctx, env.Payload))
}

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

func jobStatus(err error) uint32 {
	if err == nil {
		return 0
	}
	if jobs.IsPermanentError(err) {
		return 2
	}
	return 1
}
