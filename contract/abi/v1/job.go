package abi

// MigrationJobPayload is the wire shape a data migration job carries into a
// module's handle_job export.
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
