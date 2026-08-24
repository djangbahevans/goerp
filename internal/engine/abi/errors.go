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

// host.orm error codes (host-abi-reference.md §5a "host.orm.search"/
// "host.orm.search_read"/"host.orm.read").
const (
	ErrCodeModelNotFound      = "orm.model_not_found"
	ErrCodeDomainInvalid      = "orm.domain_invalid"
	ErrCodeFieldNotSearchable = "orm.field_not_searchable"
	ErrCodeFieldUnknown       = "orm.field_unknown"
	ErrCodeNotFound           = "orm.not_found"
)

// host.event error codes (host-abi-reference.md "host.event.emit_tx").
const (
	ErrCodeNoTransaction = "event.no_transaction"
	ErrCodeUndeclared    = "event.undeclared"
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
