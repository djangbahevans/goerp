// Package abi defines msgpack wire types shared by host functions and modules, including
// errors and response envelopes. It imports only the standard library so it builds for
// both the host and WASI.
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

const (
	ErrCodeCapabilityDenied = "abi.capability_denied"
	ErrCodeTenantIsolation  = "abi.tenant_isolation"
	ErrCodeMemoryFault      = "abi.memory_fault"
	ErrCodeAllocationFailed = "abi.allocation_failed"
	ErrCodeDeserializeError = "abi.deserialize_error"
	ErrCodeTimeout          = "abi.timeout"
	ErrCodeUnavailable      = "abi.unavailable"
)

const (
	ErrCodeAuthzForbidden        = "authz.forbidden"
	ErrCodeAuthzResourceNotFound = "authz.resource_not_found"
	ErrCodeAuthzPolicyEvaluation = "authz.policy_evaluation_failed"
)

const (
	ErrCodeTransactionAlreadyOpen   = "db.transaction_already_open"
	ErrCodeTransactionLimitExceeded = "db.transaction_limit_exceeded"
	ErrCodeTransactionNotFound      = "db.transaction_not_found"
	ErrCodeTransactionManaged       = "db.transaction_managed"
	ErrCodeCommitFailed             = "db.commit_failed"
)

// Query timeouts use db.timeout rather than the generic ABI timeout code.
const (
	ErrCodeQueryError         = "db.query_error"
	ErrCodeDBTimeout          = "db.timeout"
	ErrCodeTableAccessDenied  = "db.table_access_denied"
	ErrCodeResultTooLarge     = "db.result_too_large"
	ErrCodeReplicaUnavailable = "db.replica_unavailable"
)

// Raw SQL write errors use the db prefix; ORM writes use orm codes for the same underlying
// Postgres failures.
const (
	ErrCodeExecError             = "db.exec_error"
	ErrCodeNoRowsAffected        = "db.no_rows_affected"
	ErrCodeDBUniqueViolation     = "db.unique_violation"
	ErrCodeDBEtagMismatch        = "db.etag_mismatch"
	ErrCodeDBForeignKeyViolation = "db.foreign_key_violation"
)

const (
	ErrCodeDBBatchError        = "db.batch_error"
	ErrCodeDBBatchPartialError = "db.batch_partial_error"
)

// host.db.migration_ddl error codes for explicit-consent column and table drops in data
// migration handlers.
const (
	ErrCodeMigrationDDLError          = "db.migration_ddl_error"
	ErrCodeMigrationDDLNotOwned       = "db.migration_ddl_not_owned"
	ErrCodeMigrationDDLNotInContext   = "db.migration_ddl_not_in_migration_context"
	ErrCodeMigrationDDLTargetNotFound = "db.migration_ddl_target_not_found"
)

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
	// ErrCodeORMTimeout reports cancellation by GOERP_ORM_STATEMENT_TIMEOUT in a
	// transaction owned by ORM, distinct from a host.db call's timeout_ms.
	ErrCodeORMTimeout = "orm.timeout"
)

const (
	ErrCodeCacheValueTooLarge = "cache.value_too_large"
	ErrCodeCacheInvalidTTL    = "cache.invalid_ttl"
)

const (
	ErrCodeTransientNotListable = "orm.transient_not_listable"
)

const (
	// ErrCodeInvalidTransition rejects a workflow-transition action
	// invoked while the record's current state isn't the transition's
	// declared From state.
	ErrCodeInvalidTransition = "orm.invalid_transition"
)

const (
	ErrCodeNoTransaction  = "event.no_transaction"
	ErrCodeUndeclared     = "event.undeclared"
	ErrCodeSyncNotAllowed = "event.sync_not_allowed"
	ErrCodeDispatchFailed = "event.dispatch_failed"
)

const (
	ErrCodeJobsUndeclaredType   = "jobs.undeclared_type"
	ErrCodeJobsPayloadTooLarge  = "jobs.payload_too_large"
	ErrCodeJobsQueueUnavailable = "jobs.queue_unavailable"
	// ErrCodeJobsInvalidOptions rejects an unknown queue, a priority
	// outside 1-100, a negative delay or max_attempts, or delay_ms
	// combined with scheduled_at.
	ErrCodeJobsInvalidOptions = "jobs.invalid_options"
	// Provider resolution fails when no module provides the category or when multiple
	// enabled modules provide it without a tenant primary selection.
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

const (
	ErrCodeIndexNotFound = "search.index_not_found"
)

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
	// ErrCodeNotifyLockTimeout rejects a keyed notification insert that
	// times out waiting for a lock; retry is false.
	ErrCodeNotifyLockTimeout = "notify.lock_timeout"
	// ErrCodeNotifyDeliveryNotFound reports a missing notification, channel and
	// recipient combination in the calling tenant.
	ErrCodeNotifyDeliveryNotFound = "notify.delivery_not_found"
)

const ErrCodeConnectorInboxNotFound = "connector.inbox_not_found"
