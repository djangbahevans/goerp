package abi

import (
	"errors"

	abiv1 "github.com/djangbahevans/goerp/contract/abi/v1"
)

// ErrAllocationFailed is returned when a module's allocate export returns 0,
// signaling the module itself could not satisfy the allocation.
var ErrAllocationFailed = errors.New("abi.allocation_failed")

func CapabilityDenied(capability string) *abiv1.HostError {
	return &abiv1.HostError{
		Code:    abiv1.ErrCodeCapabilityDenied,
		Message: "module did not declare capability " + capability,
	}
}

func DeserializeError(err error) *abiv1.HostError {
	return &abiv1.HostError{
		Code:    abiv1.ErrCodeDeserializeError,
		Message: err.Error(),
	}
}

func MemoryFault() *abiv1.HostError {
	return &abiv1.HostError{
		Code:    abiv1.ErrCodeMemoryFault,
		Message: "pointer/length exceeds module linear memory",
	}
}
