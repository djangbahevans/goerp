package abi

import (
	"errors"

	abiv1 "github.com/djangbahevans/goerp/contract/abi/v1"
)

// ErrAllocationFailed is returned when a module's allocate export returns 0,
// signaling the module itself could not satisfy the allocation.
var ErrAllocationFailed = errors.New("abi.allocation_failed")

// HostError is the msgpack-serialized shape every host function returns in
// place of a normal response on failure (host-abi-reference.md §3 "Error
// model"). Defined in contract/abi/v1 and shared with the SDK.
type HostError = abiv1.HostError

// The error codes are defined in contract/abi/v1 and re-exported here
// (host-abi-reference.md §3 "Standard error codes").
const (
	ErrCodeCapabilityDenied           = abiv1.ErrCodeCapabilityDenied
	ErrCodeTenantIsolation            = abiv1.ErrCodeTenantIsolation
	ErrCodeMemoryFault                = abiv1.ErrCodeMemoryFault
	ErrCodeAllocationFailed           = abiv1.ErrCodeAllocationFailed
	ErrCodeDeserializeError           = abiv1.ErrCodeDeserializeError
	ErrCodeTimeout                    = abiv1.ErrCodeTimeout
	ErrCodeUnavailable                = abiv1.ErrCodeUnavailable
	ErrCodeTransactionAlreadyOpen     = abiv1.ErrCodeTransactionAlreadyOpen
	ErrCodeTransactionLimitExceeded   = abiv1.ErrCodeTransactionLimitExceeded
	ErrCodeTransactionNotFound        = abiv1.ErrCodeTransactionNotFound
	ErrCodeCommitFailed               = abiv1.ErrCodeCommitFailed
	ErrCodeQueryError                 = abiv1.ErrCodeQueryError
	ErrCodeDBTimeout                  = abiv1.ErrCodeDBTimeout
	ErrCodeTableAccessDenied          = abiv1.ErrCodeTableAccessDenied
	ErrCodeResultTooLarge             = abiv1.ErrCodeResultTooLarge
	ErrCodeReplicaUnavailable         = abiv1.ErrCodeReplicaUnavailable
	ErrCodeExecError                  = abiv1.ErrCodeExecError
	ErrCodeNoRowsAffected             = abiv1.ErrCodeNoRowsAffected
	ErrCodeDBUniqueViolation          = abiv1.ErrCodeDBUniqueViolation
	ErrCodeDBEtagMismatch             = abiv1.ErrCodeDBEtagMismatch
	ErrCodeDBForeignKeyViolation      = abiv1.ErrCodeDBForeignKeyViolation
	ErrCodeDBBatchError               = abiv1.ErrCodeDBBatchError
	ErrCodeDBBatchPartialError        = abiv1.ErrCodeDBBatchPartialError
	ErrCodeMigrationDDLError          = abiv1.ErrCodeMigrationDDLError
	ErrCodeMigrationDDLNotOwned       = abiv1.ErrCodeMigrationDDLNotOwned
	ErrCodeMigrationDDLNotInContext   = abiv1.ErrCodeMigrationDDLNotInContext
	ErrCodeMigrationDDLTargetNotFound = abiv1.ErrCodeMigrationDDLTargetNotFound
	ErrCodeModelNotFound              = abiv1.ErrCodeModelNotFound
	ErrCodeDomainInvalid              = abiv1.ErrCodeDomainInvalid
	ErrCodeFieldNotSearchable         = abiv1.ErrCodeFieldNotSearchable
	ErrCodeFieldUnknown               = abiv1.ErrCodeFieldUnknown
	ErrCodeNotFound                   = abiv1.ErrCodeNotFound
	ErrCodeFieldReadDenied            = abiv1.ErrCodeFieldReadDenied
	ErrCodeValidationFailed           = abiv1.ErrCodeValidationFailed
	ErrCodeUniqueViolation            = abiv1.ErrCodeUniqueViolation
	ErrCodeEtagMismatch               = abiv1.ErrCodeEtagMismatch
	ErrCodePreconditionFailed         = abiv1.ErrCodePreconditionFailed
	ErrCodeForeignKeyViolation        = abiv1.ErrCodeForeignKeyViolation
	ErrCodeConflictTargetInvalid      = abiv1.ErrCodeConflictTargetInvalid
	ErrCodeFieldNotWritable           = abiv1.ErrCodeFieldNotWritable
	ErrCodeFieldWriteDenied           = abiv1.ErrCodeFieldWriteDenied
	ErrCodeCycleDetected              = abiv1.ErrCodeCycleDetected
	ErrCodeDynamicLinkTargetNotFound  = abiv1.ErrCodeDynamicLinkTargetNotFound
	ErrCodeBatchTooLarge              = abiv1.ErrCodeBatchTooLarge
	ErrCodeORMTimeout                 = abiv1.ErrCodeORMTimeout
	ErrCodeTransientNotListable       = abiv1.ErrCodeTransientNotListable
	ErrCodeInvalidTransition          = abiv1.ErrCodeInvalidTransition
	ErrCodeNoTransaction              = abiv1.ErrCodeNoTransaction
	ErrCodeUndeclared                 = abiv1.ErrCodeUndeclared
	ErrCodeSyncNotAllowed             = abiv1.ErrCodeSyncNotAllowed
	ErrCodeDispatchFailed             = abiv1.ErrCodeDispatchFailed
	ErrCodeIndexNotFound              = abiv1.ErrCodeIndexNotFound
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
