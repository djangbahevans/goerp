// Package hostcall implements the request/response envelope every
// module-to-host call shares (host-abi-reference.md §2 "Boundary
// mechanics", §3 "Error handling"): marshal a request, invoke the host
// import, unpack the returned (ptr,len) i64, and decode the
// {ok,data,error} envelope into either a typed response or an abi.HostError.
//
// Each host.* function needs its own //go:wasmimport-declared Go function
// (the compiler directive can't be parameterized at runtime), so this
// package doesn't call the import itself — sdk/go/db, sdk/go/events, and
// friends each declare their own low-level imports and pass them in as an
// Invoke, building one typed wrapper per host function on this one shared
// mechanism.
package hostcall

import (
	"errors"
	"fmt"

	abi "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/djangbahevans/goerp/sdk/go/internal/wasmmem"
	"github.com/vmihailenco/msgpack/v5"
)

// Invoke is the raw wasmimport-shaped host call: request bytes written
// into the module's own memory at (ptr,size), packed (ptr<<32|len) i64
// response out.
type Invoke func(ptr, size uint32) uint64

// Do marshals req, calls invoke, and decodes the response envelope into
// resp (which may be nil for a call whose success response carries no
// data). Returns the decoded *abi.HostError as a Go error on a host-side
// failure.
func Do(invoke Invoke, req any, resp any) error {
	data, err := msgpack.Marshal(req)
	if err != nil {
		return fmt.Errorf("marshal request: %w", err)
	}

	ptr := wasmmem.Allocate(uint32(len(data)))
	if ptr == 0 {
		return &abi.HostError{Code: abi.ErrCodeAllocationFailed, Message: "could not allocate request buffer"}
	}
	wasmmem.WriteMem(ptr, data)

	packed := invoke(ptr, uint32(len(data)))
	// The host has read the request by now (invoke already returned) —
	// free it so wasmmem's live registry doesn't grow unbounded across a
	// pooled module instance's many host calls over its lifetime.
	wasmmem.Deallocate(ptr, uint32(len(data)))

	respPtr := uint32(packed >> 32)
	respLen := uint32(packed)
	if respPtr == 0 {
		return &abi.HostError{Code: abi.ErrCodeAllocationFailed, Message: "host call returned a null response pointer"}
	}
	defer wasmmem.Deallocate(respPtr, respLen)

	raw := wasmmem.ReadMem(respPtr, respLen)
	var env abi.Envelope
	if err := msgpack.Unmarshal(raw, &env); err != nil {
		return fmt.Errorf("unmarshal response envelope: %w", err)
	}

	if !env.OK {
		if env.Error != nil {
			return env.Error
		}
		return errors.New("host call failed with no error detail")
	}

	if resp != nil && len(env.Data) > 0 {
		return msgpack.Unmarshal(env.Data, resp)
	}
	return nil
}
