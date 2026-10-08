package jobqueue

import "github.com/riverqueue/river"

// UserID is captured from the enqueuing handler and resolved against live tenant
// membership on each attempt. It is excluded from deduplication so a repeated key
// retains the original job's identity.
type WASMJobArgs struct {
	ModuleName     string `json:"module_name" river:"unique"`
	JobType        string `json:"job_type" river:"unique"`
	Payload        []byte `json:"payload"`
	TenantID       string `json:"tenant_id" river:"unique"`
	UserID         string `json:"user_id,omitempty"`
	Queue          string `json:"queue"`
	MaxAttempts    int    `json:"max_attempts"`
	IdempotencyKey string `json:"idempotency_key,omitempty" river:"unique"`
	TraceID        string `json:"trace_id,omitempty"`
	// IsCron marks a cron job: JobType is the cron job's name, declared in the
	// module's cron_jobs rather than job_types, and the handler runs through
	// handle_cron with an empty payload.
	IsCron bool `json:"is_cron,omitempty" river:"unique"`
	// Migration handlers are validated against DataMigrations rather than JobRegistry.
	// Their version watermark advances only after the handler succeeds.
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
	// Engine-created delivery jobs track notification outcomes; module-enqueued
	// provider jobs do not carry NotificationID.
	NotificationID string `json:"notification_id,omitempty"`
}

func (WASMJobArgs) Kind() string { return "wasm_job" }

func (a WASMJobArgs) InsertOpts() river.InsertOpts {
	queue := a.Queue
	if queue == "" {
		queue = QueueDefault
	}
	return river.InsertOpts{Queue: queue, MaxAttempts: a.MaxAttempts}
}

// Provider jobs distinguish the handling module from the module that enqueued them.
type JobMetadata struct {
	TenantID   string `json:"tenant_id"`
	ModuleName string `json:"module_name"`
	TraceID    string `json:"trace_id,omitempty"`
	EnqueuedBy string `json:"enqueued_by,omitempty"`
}
