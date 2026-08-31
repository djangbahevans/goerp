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
// host.jobs.enqueue (implementation-backlog.md #86) — a module enqueueing
// its own ordinary job — is still separate, unfiled work. The first real
// inserter of a WASMJobArgs job is goerp#114's engine-triggered data
// migration dispatch (IsDataMigration below), which is a different trust
// boundary: an ordinary job's JobType must be declared in the enqueuing
// module's own manifest job_types[] (checked against JobRegistry, which
// enforces name uniqueness across every module), while a data migration's
// JobType is one of the target module's own declared DataMigrations[].Handler
// names — a namespace that's scoped per-module and never required to be
// globally unique, since two unrelated modules may both happen to name a
// handler "backfill_display_name". Queue and MaxAttempts are set by
// whichever caller eventually does the inserting, sourced from the target
// job type's declared manifest.JobType — this type only carries them
// through to InsertOpts, it doesn't look them up.
type WASMJobArgs struct {
	ModuleName  string `json:"module_name"`
	JobType     string `json:"job_type"`
	Payload     []byte `json:"payload"`
	TenantID    string `json:"tenant_id"`
	Queue       string `json:"queue"`
	MaxAttempts int    `json:"max_attempts"`
	// IsDataMigration marks a job the engine itself enqueued to run one of
	// the target module's declared data migration handlers during an
	// upgrade (migration-guide.md §4), rather than a module-authored
	// ordinary job. jobdispatch.Worker checks JobType against a different
	// name space for this class (see above), and — once the handler
	// succeeds — advances the tenant's data_migration_version watermark to
	// MigrationToVersion and enqueues the next applicable handler, if any.
	IsDataMigration bool `json:"is_data_migration,omitempty"`
	// MigrationToVersion is the concrete version (schema.MigrationBoundaryVersion
	// of the migration's declared ToVersion) to record as the tenant's new
	// data_migration_version watermark once this job succeeds. Only
	// meaningful when IsDataMigration is true.
	MigrationToVersion string `json:"migration_to_version,omitempty"`
	// MigrationFromVersion is the tenant's data_migration_version watermark
	// at the moment this job was enqueued (EnqueueApplicableDataMigration's
	// own "watermark" value) — carried alongside MigrationToVersion so
	// Payload's own model.MigrationJobPayload can report both bounds to the
	// handler as model.MigrationContext.FromVersion/ToVersion. Only
	// meaningful when IsDataMigration is true.
	MigrationFromVersion string `json:"migration_from_version,omitempty"`
}

func (WASMJobArgs) Kind() string { return "wasm_job" }

func (a WASMJobArgs) InsertOpts() river.InsertOpts {
	queue := a.Queue
	if queue == "" {
		queue = QueueDefault
	}
	return river.InsertOpts{Queue: queue, MaxAttempts: a.MaxAttempts}
}
