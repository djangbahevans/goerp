// Package abi is the single definition of the msgpack wire types exchanged
// between engine host functions and the SDK: the error model and the
// {ok, data, error} response envelope every host function returns
// (host-abi-reference.md §2 "Boundary mechanics", §3 "Error model").
//
// It imports only the standard library so both the engine and a module built
// for GOOS=wasip1 can depend on it. Changes within v1 are additive; a
// breaking change is a v2 package together with an abi_version bump.
package abi

// HostError is the msgpack-serialized shape every host function returns in
// place of a normal response on failure.
type HostError struct {
	Code    string         `msgpack:"code"`
	Message string         `msgpack:"message"`
	Details map[string]any `msgpack:"details,omitempty"`
	Retry   bool           `msgpack:"retry"`
}

func (e *HostError) Error() string {
	return e.Code + ": " + e.Message
}

// Standard error codes any host function can return (host-abi-reference.md
// §3 "Standard error codes").
const (
	ErrCodeCapabilityDenied = "abi.capability_denied"
	ErrCodeTenantIsolation  = "abi.tenant_isolation"
	ErrCodeMemoryFault      = "abi.memory_fault"
	ErrCodeAllocationFailed = "abi.allocation_failed"
	ErrCodeDeserializeError = "abi.deserialize_error"
	ErrCodeTimeout          = "abi.timeout"
	ErrCodeUnavailable      = "abi.unavailable"
)

// host.db error codes (host-abi-reference.md §5 "host.db.begin"/"commit"/"rollback").
const (
	ErrCodeTransactionAlreadyOpen   = "db.transaction_already_open"
	ErrCodeTransactionLimitExceeded = "db.transaction_limit_exceeded"
	ErrCodeTransactionNotFound      = "db.transaction_not_found"
	ErrCodeCommitFailed             = "db.commit_failed"
)

// host.db.query/host.db.query_replica error codes (host-abi-reference.md §5
// "host.db.query"/"host.db.query_replica"). ErrCodeDBTimeout is distinct
// from the generic ErrCodeTimeout above — the doc documents "db.timeout"
// specifically for a query exceeding its own timeout_ms, not the general
// ABI-wide "abi.timeout".
const (
	ErrCodeQueryError         = "db.query_error"
	ErrCodeDBTimeout          = "db.timeout"
	ErrCodeTableAccessDenied  = "db.table_access_denied"
	ErrCodeResultTooLarge     = "db.result_too_large"
	ErrCodeReplicaUnavailable = "db.replica_unavailable"
)

// host.db.exec error codes (host-abi-reference.md §5 "host.db.exec").
// Distinct from the host.orm write codes below despite covering the same
// underlying Postgres errors (unique/FK violation, etag mismatch): the
// doc documents these under the "db." prefix specifically for exec's raw
// SQL path, not "orm.".
const (
	ErrCodeExecError             = "db.exec_error"
	ErrCodeNoRowsAffected        = "db.no_rows_affected"
	ErrCodeDBUniqueViolation     = "db.unique_violation"
	ErrCodeDBEtagMismatch        = "db.etag_mismatch"
	ErrCodeDBForeignKeyViolation = "db.foreign_key_violation"
)

// host.db.exec_batch error codes (host-abi-reference.md §5
// "host.db.exec_batch").
const (
	ErrCodeDBBatchError        = "db.batch_error"
	ErrCodeDBBatchPartialError = "db.batch_partial_error"
)

// host.db.migration_ddl error codes (host-abi-reference.md §5
// "host.db.migration_ddl") — the explicit-consent DropColumn/DropTable
// escape hatch for data migration handlers (migration-guide.md §4,
// goerp#500).
const (
	ErrCodeMigrationDDLError          = "db.migration_ddl_error"
	ErrCodeMigrationDDLNotOwned       = "db.migration_ddl_not_owned"
	ErrCodeMigrationDDLNotInContext   = "db.migration_ddl_not_in_migration_context"
	ErrCodeMigrationDDLTargetNotFound = "db.migration_ddl_target_not_found"
)

// host.orm error codes (host-abi-reference.md §5a "host.orm.search"/
// "host.orm.search_read"/"host.orm.read").
const (
	ErrCodeModelNotFound      = "orm.model_not_found"
	ErrCodeDomainInvalid      = "orm.domain_invalid"
	ErrCodeFieldNotSearchable = "orm.field_not_searchable"
	ErrCodeFieldUnknown       = "orm.field_unknown"
	ErrCodeNotFound           = "orm.not_found"
	// ErrCodeFieldReadDenied rejects a whole aggregate/pivot request when
	// a rows/columns/values field has a read rule the caller fails —
	// aggregation has no per-record mask/nullify/omit fallback to use.
	ErrCodeFieldReadDenied = "orm.field_read_denied"
)

// host.orm write error codes (host-abi-reference.md §5a "host.orm.create"/
// "host.orm.write"/"host.orm.unlink").
const (
	ErrCodeValidationFailed          = "orm.validation_failed"
	ErrCodeUniqueViolation           = "orm.unique_violation"
	ErrCodeEtagMismatch              = "orm.etag_mismatch"
	ErrCodePreconditionFailed        = "orm.precondition_failed"
	ErrCodeForeignKeyViolation       = "orm.foreign_key_violation"
	ErrCodeConflictTargetInvalid     = "orm.conflict_target_invalid"
	ErrCodeFieldNotWritable          = "orm.field_not_writable"
	ErrCodeFieldWriteDenied          = "orm.field_write_denied"
	ErrCodeCycleDetected             = "orm.cycle_detected"
	ErrCodeDynamicLinkTargetNotFound = "orm.dynamic_link_target_not_found"
	// ErrCodeBatchTooLarge rejects a create_batch/write_many/write_where/
	// unlink call whose records/IDs (or, for write_where, matched rows)
	// exceed GOERP_ORM_BULK_MAX_ROWS — before any write, Details carry
	// "limit" and "count".
	ErrCodeBatchTooLarge = "orm.batch_too_large"
	// ErrCodeORMTimeout is distinct from the generic ErrCodeTimeout and
	// from ErrCodeDBTimeout above — a statement of an ORM-owned
	// transaction cancelled by GOERP_ORM_STATEMENT_TIMEOUT (SQLSTATE
	// 57014), not a host.db.query call's own timeout_ms.
	ErrCodeORMTimeout = "orm.timeout"
)

// host.cache error codes (host-abi-reference.md §7 "host.cache.set").
const (
	ErrCodeCacheValueTooLarge = "cache.value_too_large"
	ErrCodeCacheInvalidTTL    = "cache.invalid_ttl"
)

// Transient-model error codes (go-sdk-reference.md §22 "Transient models").
const (
	ErrCodeTransientNotListable = "orm.transient_not_listable"
)

// Workflow-transition error codes (go-sdk-reference.md "Declarative
// workflow transitions").
const (
	// ErrCodeInvalidTransition rejects a workflow-transition action
	// invoked while the record's current state isn't the transition's
	// declared From state.
	ErrCodeInvalidTransition = "orm.invalid_transition"
)

// host.event error codes (host-abi-reference.md "host.event.emit_tx"/
// "host.event.emit").
const (
	ErrCodeNoTransaction  = "event.no_transaction"
	ErrCodeUndeclared     = "event.undeclared"
	ErrCodeSyncNotAllowed = "event.sync_not_allowed"
	ErrCodeDispatchFailed = "event.dispatch_failed"
)

// host.jobs error codes (host-abi-reference.md §10 "host.jobs.enqueue").
const (
	ErrCodeJobsUndeclaredType   = "jobs.undeclared_type"
	ErrCodeJobsPayloadTooLarge  = "jobs.payload_too_large"
	ErrCodeJobsQueueUnavailable = "jobs.queue_unavailable"
	// ErrCodeJobsInvalidOptions rejects an unknown queue, a priority
	// outside 1-100, a negative delay or max_attempts, or delay_ms
	// combined with scheduled_at.
	ErrCodeJobsInvalidOptions = "jobs.invalid_options"
	// ErrCodeJobsNoProviderSelected and ErrCodeJobsNoProviderInstalled are
	// single-active provider resolution's two failures (connector-guide.md
	// §7): more than one enabled module provides the category and none is
	// the tenant's primary, or none provides it at all.
	ErrCodeJobsNoProviderSelected  = "jobs.no_provider_selected"
	ErrCodeJobsNoProviderInstalled = "jobs.no_provider_installed"
	// ErrCodeJobsProviderModuleNotEnabled rejects an explicit
	// provider_module that isn't installed, enabled and providing the
	// category for the tenant.
	ErrCodeJobsProviderModuleNotEnabled = "jobs.provider_module_not_enabled"
	// ErrCodeJobsInvalidProviderCategory rejects a category that isn't a
	// provider category, and payment_provider without a provider_module:
	// a multi-active category is never resolved.
	ErrCodeJobsInvalidProviderCategory = "jobs.invalid_provider_category"
	// ErrCodeJobsSyncInTransaction rejects host.jobs.dispatch_provider_sync
	// while the caller has a host.db transaction open.
	ErrCodeJobsSyncInTransaction = "jobs.sync_in_transaction"
	// ErrCodeJobsSyncDispatchTimeout: the synchronously dispatched handler
	// did not return within timeout_ms.
	ErrCodeJobsSyncDispatchTimeout = "jobs.sync_dispatch_timeout"
	// ErrCodeJobsHandlerFailed: the synchronously dispatched handler
	// returned a failure status or trapped.
	ErrCodeJobsHandlerFailed = "jobs.handler_failed"
)

// host.search error codes (host-abi-reference.md §12 "host.search.query").
const (
	ErrCodeIndexNotFound = "search.index_not_found"
)

// host.config error codes (host-abi-reference.md §14 "host.config.get"/
// "host.config.set").
const (
	// ErrCodeConfigKeyUndeclared rejects a get whose key is neither a short
	// key in the caller's own config_schema nor a "{module}.{key}" name in
	// its uses_config, and a set whose key is not in its own config_schema
	// — a module can never write another module's config.
	ErrCodeConfigKeyUndeclared = "config.key_undeclared"
	// ErrCodeConfigEncryptionError covers both directions of an
	// "encrypted": true key's AES-256-GCM round trip (rowcrypt.RowKeySet
	// Encrypt/Decrypt) — a malformed or unrecognized-key-id ciphertext on
	// get, or an encrypt failure on set.
	ErrCodeConfigEncryptionError = "config.encryption_error"
)

// host.notify error codes (host-abi-reference.md §11 "host.notify.send"/
// "host.notify.send_bulk").
const (
	// ErrCodeNotifyUndeclaredType rejects a type that isn't
	// "{caller's own module}.{a name in its own notification_types}" — a
	// module can never send another module's type.
	ErrCodeNotifyUndeclaredType = "notify.undeclared_type"
	// ErrCodeNotifyUnknownRecipient rejects a user_id that isn't a live
	// user who belongs to the tenant.
	ErrCodeNotifyUnknownRecipient = "notify.unknown_recipient"
	// ErrCodeNotifyInvalidOptions rejects a priority other than normal or
	// high, or an option naming something that isn't a channel.
	ErrCodeNotifyInvalidOptions = "notify.invalid_options"
	// ErrCodeNotifyTooManyRecipients rejects a send_bulk over 1000
	// distinct user_ids; a caller with more batches them through
	// host.jobs.enqueue.
	ErrCodeNotifyTooManyRecipients = "notify.too_many_recipients"
	// ErrCodeNotifyRenderFailed rejects data the notification type's
	// in_app template cannot render against; resending the same data
	// fails the same way.
	ErrCodeNotifyRenderFailed = "notify.render_failed"
)

// host.connector error code (host-abi-reference.md §10a).
const ErrCodeConnectorInboxNotFound = "connector.inbox_not_found"
