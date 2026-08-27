//go:build !wasip1

// Non-wasip1 builds back Allocate/Deallocate/ReadMem/WriteMem with an
// in-process byte-slice arena instead of real linear memory — lets
// module-side code that only touches memory (not a real host.* import)
// run under a normal `go test` binary. See mem_wasip1.go's package doc
// for why this lives in its own package.
package wasmmem

import "sync"

var (
	memMu   sync.Mutex
	memNext uint32 = 1
	mem            = map[uint32][]byte{}
)

func Allocate(size uint32) uint32 {
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

func Deallocate(ptr, _ uint32) {
	memMu.Lock()
	defer memMu.Unlock()
	delete(mem, ptr)
}

func ReadMem(ptr, size uint32) []byte {
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

func WriteMem(ptr uint32, data []byte) {
	memMu.Lock()
	defer memMu.Unlock()

	buf, ok := mem[ptr]
	if !ok {
		return
	}
	copy(buf, data)
}
