//go:build !wasip1

// Non-wasip1 builds back allocate/deallocate/readMem/writeMem via
// sdk/go/internal/wasmmem's in-process byte-slice arena instead of real
// linear memory. This lets the exact same DispatchRequest/router/response
// code a module runs under WASM be driven directly from a normal `go
// test` binary — that's the mock-host: no real WASM instantiation
// involved.
package engine

import "github.com/djangbahevans/goerp/sdk/go/internal/wasmmem"

func allocate(size uint32) uint32 { return wasmmem.Allocate(size) }

func deallocate(ptr, size uint32) { wasmmem.Deallocate(ptr, size) }

func readMem(ptr, size uint32) []byte { return wasmmem.ReadMem(ptr, size) }

func writeMem(ptr uint32, data []byte) { wasmmem.WriteMem(ptr, data) }
