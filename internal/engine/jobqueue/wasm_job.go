package jobqueue

import "github.com/riverqueue/river"

// WASMJobArgs is the River job jobdispatch.Worker (internal/engine/
// jobdispatch — lives outside this package for the same import-cycle
// reason eventdelivery.Worker does, see that package's own doc comment)
// processes by invoking ModuleName's handle_job WASM export
// (manifest-spec.md §26, goerp#110). One Kind covers every WASM-dispatched
// job type, discriminated by ModuleName/JobType, the same way
// SubscriberDeliveryArgs is one Kind for every subscriber delivery rather
// than one per handler.
//
// Nothing in this codebase inserts a WASMJobArgs job yet — host.jobs.enqueue
// (implementation-backlog.md #86) is separate, unfiled work. Queue and
// MaxAttempts are set by whichever caller eventually does the inserting,
// sourced from the target job type's declared manifest.JobType — this
// type only carries them through to InsertOpts, it doesn't look them up.
type WASMJobArgs struct {
	ModuleName  string `json:"module_name"`
	JobType     string `json:"job_type"`
	Payload     []byte `json:"payload"`
	TenantID    string `json:"tenant_id"`
	Queue       string `json:"queue"`
	MaxAttempts int    `json:"max_attempts"`
}

func (WASMJobArgs) Kind() string { return "wasm_job" }

func (a WASMJobArgs) InsertOpts() river.InsertOpts {
	queue := a.Queue
	if queue == "" {
		queue = QueueDefault
	}
	return river.InsertOpts{Queue: queue, MaxAttempts: a.MaxAttempts}
}
