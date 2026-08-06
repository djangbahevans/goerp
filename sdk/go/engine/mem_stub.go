//go:build !wasip1

// Non-wasip1 builds back allocate/deallocate/readMem/writeMem with an
// in-process byte-slice arena instead of real linear memory. This lets the
// exact same DispatchRequest/router/response code a module runs under WASM
// be driven directly from a normal `go test` binary — that's the mock-host:
// no real WASM instantiation involved.
package engine

import "sync"

var (
	memMu   sync.Mutex
	memNext uint32 = 1
	mem            = map[uint32][]byte{}
)

func allocate(size uint32) uint32 {
	memMu.Lock()
	defer memMu.Unlock()

	ptr := memNext
	memNext += size
	if memNext == 0 {
		memNext = 1
	}
	mem[ptr] = make([]byte, size)
	return ptr
}

func deallocate(ptr, _ uint32) {
	memMu.Lock()
	defer memMu.Unlock()
	delete(mem, ptr)
}

func readMem(ptr, size uint32) []byte {
	memMu.Lock()
	defer memMu.Unlock()

	buf, ok := mem[ptr]
	if !ok {
		return nil
	}
	out := make([]byte, size)
	copy(out, buf)
	return out
}

func writeMem(ptr uint32, data []byte) {
	memMu.Lock()
	defer memMu.Unlock()

	buf, ok := mem[ptr]
	if !ok {
		return
	}
	copy(buf, data)
}
