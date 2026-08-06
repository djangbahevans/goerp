package abi

import "errors"

// ErrAllocationFailed is returned when a module's allocate export returns 0,
// signaling the module itself could not satisfy the allocation.
var ErrAllocationFailed = errors.New("abi.allocation_failed")
