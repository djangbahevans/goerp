package abi

import "errors"

// ErrAllocationFailed is returned when a module's allocate export returns 0,
// signaling the module itself could not satisfy the allocation.
var ErrAllocationFailed = errors.New("abi.allocation_failed")

// HostError is the msgpack-serialized shape every host function returns in
// place of a normal response on failure (host-abi-reference.md §3 "Error
// model").
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

// host.orm error codes (host-abi-reference.md §5a "host.orm.search"/
// "host.orm.search_read"/"host.orm.read").
const (
	ErrCodeModelNotFound      = "orm.model_not_found"
	ErrCodeDomainInvalid      = "orm.domain_invalid"
	ErrCodeFieldNotSearchable = "orm.field_not_searchable"
	ErrCodeFieldUnknown       = "orm.field_unknown"
	ErrCodeNotFound           = "orm.not_found"
)

// host.orm write error codes (host-abi-reference.md §5a "host.orm.create"/
// "host.orm.write"/"host.orm.unlink").
const (
	ErrCodeValidationFailed          = "orm.validation_failed"
	ErrCodeUniqueViolation           = "orm.unique_violation"
	ErrCodeEtagMismatch              = "orm.etag_mismatch"
	ErrCodeForeignKeyViolation       = "orm.foreign_key_violation"
	ErrCodeConflictTargetInvalid     = "orm.conflict_target_invalid"
	ErrCodeFieldNotWritable          = "orm.field_not_writable"
	ErrCodeFieldWriteDenied          = "orm.field_write_denied"
	ErrCodeCycleDetected             = "orm.cycle_detected"
	ErrCodeDynamicLinkTargetNotFound = "orm.dynamic_link_target_not_found"
)

// Transient-model error codes (go-sdk-reference.md §22 "Transient models").
const (
	ErrCodeTransientNotListable = "orm.transient_not_listable"
)

// host.event error codes (host-abi-reference.md "host.event.emit_tx"/
// "host.event.emit").
const (
	ErrCodeNoTransaction  = "event.no_transaction"
	ErrCodeUndeclared     = "event.undeclared"
	ErrCodeSyncNotAllowed = "event.sync_not_allowed"
	ErrCodeDispatchFailed = "event.dispatch_failed"
)

// host.search error codes (host-abi-reference.md §12 "host.search.query").
const (
	ErrCodeIndexNotFound = "search.index_not_found"
)

func CapabilityDenied(capability string) *HostError {
	return &HostError{
		Code:    ErrCodeCapabilityDenied,
		Message: "module did not declare capability " + capability,
	}
}

func DeserializeError(err error) *HostError {
	return &HostError{
		Code:    ErrCodeDeserializeError,
		Message: err.Error(),
	}
}

func MemoryFault() *HostError {
	return &HostError{
		Code:    ErrCodeMemoryFault,
		Message: "pointer/length exceeds module linear memory",
	}
}
