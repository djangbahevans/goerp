package jobqueue

import "github.com/riverqueue/river"

// WASMJobArgs is the River job jobdispatch.Worker processes by invoking
// ModuleName's handle_job WASM export (manifest-spec.md §26). One Kind
// covers every WASM-dispatched job type, discriminated by
// ModuleName/JobType.
//
// Three callers insert these, across different trust boundaries:
// host.jobs.enqueue/enqueue_tx, whose JobType must be declared in the
// enqueuing module's own manifest job_types[] (globally unique, enforced by
// JobRegistry); host.jobs.enqueue_provider/enqueue_provider_tx, whose
// JobType is a standardized provider-category name and whose ModuleName is
// the resolved provider (ProviderCategory); and the engine's data migration
// dispatch, whose JobType is one of the target module's
// DataMigrations[].Handler names (scoped per-module, never required to be
// globally unique).
//
// The river:"unique" fields define what River's ByArgs dedup hashes:
// everything identifying the job except Payload, Queue, MaxAttempts and
// TraceID, so a re-enqueue under the same IdempotencyKey dedups even when
// its payload or trace differs.
type WASMJobArgs struct {
	ModuleName     string `json:"module_name" river:"unique"`
	JobType        string `json:"job_type" river:"unique"`
	Payload        []byte `json:"payload"`
	TenantID       string `json:"tenant_id" river:"unique"`
	Queue          string `json:"queue"`
	MaxAttempts    int    `json:"max_attempts"`
	IdempotencyKey string `json:"idempotency_key,omitempty" river:"unique"`
	TraceID        string `json:"trace_id,omitempty"`
	// IsDataMigration marks a job the engine itself enqueued to run one of
	// the target module's declared data migration handlers during an
	// upgrade (migration-guide.md §4). jobdispatch.Worker checks JobType
	// against the module's DataMigrations for this class and, once the
	// handler succeeds, advances the tenant's data_migration_version
	// watermark to MigrationToVersion and enqueues the next applicable
	// handler, if any.
	IsDataMigration bool `json:"is_data_migration,omitempty" river:"unique"`
	// MigrationToVersion is the watermark to record once this job
	// succeeds. Only meaningful when IsDataMigration is true.
	MigrationToVersion string `json:"migration_to_version,omitempty" river:"unique"`
	// MigrationFromVersion is the tenant's watermark when this job was
	// enqueued, reported to the handler alongside MigrationToVersion. Only
	// meaningful when IsDataMigration is true.
	MigrationFromVersion string `json:"migration_from_version,omitempty" river:"unique"`
	// ProviderCategory marks a host.jobs.enqueue_provider job: ModuleName is
	// the provider resolved at enqueue time, and JobType is in no JobRegistry.
	ProviderCategory string `json:"provider_category,omitempty" river:"unique"`
	// EnqueuedBy is set only on a provider job, so two modules' idempotency
	// keys never collide on the same provider.
	EnqueuedBy string `json:"enqueued_by,omitempty" river:"unique"`
}

func (WASMJobArgs) Kind() string { return "wasm_job" }

func (a WASMJobArgs) InsertOpts() river.InsertOpts {
	queue := a.Queue
	if queue == "" {
		queue = QueueDefault
	}
	return river.InsertOpts{Queue: queue, MaxAttempts: a.MaxAttempts}
}

// JobMetadata is stamped onto every WASMJobArgs job's River metadata, so
// the job's origin is visible without decoding its args. ModuleName is the
// module whose handler runs the job; EnqueuedBy is set only for a
// provider-category job, where that is not the enqueuing module.
type JobMetadata struct {
	TenantID   string `json:"tenant_id"`
	ModuleName string `json:"module_name"`
	TraceID    string `json:"trace_id,omitempty"`
	EnqueuedBy string `json:"enqueued_by,omitempty"`
}
