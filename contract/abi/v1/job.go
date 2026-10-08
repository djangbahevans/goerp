package abi

// UserID is the initiating user, empty for anonymous and data-migration jobs.
// A migration's Payload is a MigrationJobPayload whose Handler equals JobType.
type JobEnvelope struct {
	JobID           string `msgpack:"job_id"`
	JobType         string `msgpack:"job_type"`
	TenantID        string `msgpack:"tenant_id"`
	UserID          string `msgpack:"user_id,omitempty"`
	ModuleName      string `msgpack:"module_name"`
	TraceID         string `msgpack:"trace_id,omitempty"`
	Attempt         int    `msgpack:"attempt"`
	MaxAttempts     int    `msgpack:"max_attempts"`
	IsDataMigration bool   `msgpack:"is_data_migration,omitempty"`
	Payload         []byte `msgpack:"payload"`
}

// MigrationJobPayload is the payload of a data migration job's
// JobEnvelope.
type MigrationJobPayload struct {
	Handler     string `msgpack:"handler"`
	TenantID    string `msgpack:"tenant_id"`
	FromVersion string `msgpack:"from_version"`
	ToVersion   string `msgpack:"to_version"`
}

// JobEnqueueOptions is the opts member of host.jobs.enqueue/enqueue_tx.
// Zero values fall back to the job type's manifest declaration, then to
// the engine defaults. ScheduledAt is a unix timestamp in seconds and
// cannot be combined with DelayMs.
type JobEnqueueOptions struct {
	Queue          string `msgpack:"queue,omitempty"`
	Priority       int    `msgpack:"priority,omitempty"`
	DelayMs        int64  `msgpack:"delay_ms,omitempty"`
	MaxAttempts    int    `msgpack:"max_attempts,omitempty"`
	IdempotencyKey string `msgpack:"idempotency_key,omitempty"`
	ScheduledAt    int64  `msgpack:"scheduled_at,omitempty"`
}

// JobsEnqueueInput is the request of host.jobs.enqueue.
type JobsEnqueueInput struct {
	Type    string            `msgpack:"type"`
	Payload []byte            `msgpack:"payload"`
	Opts    JobEnqueueOptions `msgpack:"opts"`
}

// JobsEnqueueTxInput is the request of host.jobs.enqueue_tx: the shape of
// JobsEnqueueInput scoped to an open host.db transaction.
type JobsEnqueueTxInput struct {
	TxID    string            `msgpack:"tx_id"`
	Type    string            `msgpack:"type"`
	Payload []byte            `msgpack:"payload"`
	Opts    JobEnqueueOptions `msgpack:"opts"`
}

// JobsEnqueueOutput is the response of host.jobs.enqueue/enqueue_tx.
// Deduplicated is true when an existing job with the same idempotency key
// was found; JobID is then that job's ID.
type JobsEnqueueOutput struct {
	JobID        string `msgpack:"job_id"`
	Deduplicated bool   `msgpack:"deduplicated"`
}

// JobsEnqueueProviderInput dispatches a job to the tenant's active provider for Category
// or to ProviderModule when specified.
type JobsEnqueueProviderInput struct {
	Category       string            `msgpack:"category"`
	JobType        string            `msgpack:"job_type"`
	Payload        []byte            `msgpack:"payload"`
	ProviderModule string            `msgpack:"provider_module,omitempty"`
	Opts           JobEnqueueOptions `msgpack:"opts"`
}

// JobsEnqueueProviderTxInput is the request of
// host.jobs.enqueue_provider_tx: the shape of JobsEnqueueProviderInput
// scoped to an open host.db transaction.
type JobsEnqueueProviderTxInput struct {
	TxID           string            `msgpack:"tx_id"`
	Category       string            `msgpack:"category"`
	JobType        string            `msgpack:"job_type"`
	Payload        []byte            `msgpack:"payload"`
	ProviderModule string            `msgpack:"provider_module,omitempty"`
	Opts           JobEnqueueOptions `msgpack:"opts"`
}

// JobsEnqueueProviderOutput is the response of
// host.jobs.enqueue_provider/enqueue_provider_tx: JobsEnqueueOutput plus
// the module the job was dispatched to.
type JobsEnqueueProviderOutput struct {
	JobID          string `msgpack:"job_id"`
	Deduplicated   bool   `msgpack:"deduplicated"`
	ResolvedModule string `msgpack:"resolved_module"`
}

// JobsDispatchProviderSyncInput is the request of
// host.jobs.dispatch_provider_sync. TimeoutMs zero means the engine's
// GOERP_SYNC_PROVIDER_TIMEOUT default.
type JobsDispatchProviderSyncInput struct {
	Category       string `msgpack:"category"`
	ProviderModule string `msgpack:"provider_module"`
	JobType        string `msgpack:"job_type"`
	Payload        []byte `msgpack:"payload"`
	TimeoutMs      int64  `msgpack:"timeout_ms,omitempty"`
}

// JobsDispatchProviderSyncOutput is the response of
// host.jobs.dispatch_provider_sync. ResultPayload is the msgpack value the
// handler passed to host.jobs.set_result, nil when it set none.
type JobsDispatchProviderSyncOutput struct {
	ResultPayload []byte `msgpack:"result_payload,omitempty"`
}

// JobsSetResultInput is the request of host.jobs.set_result: the
// msgpack-encoded value a job handler hands back to a
// host.jobs.dispatch_provider_sync caller.
type JobsSetResultInput struct {
	Value []byte `msgpack:"value"`
}

// JobsSetResultOutput is the empty response of host.jobs.set_result.
type JobsSetResultOutput struct{}
