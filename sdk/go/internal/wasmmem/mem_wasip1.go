//go:build wasip1

// Package wasmmem is the module-side linear-memory allocator/accessor
// pair sdk/go/engine (module exports the engine calls into) and
// sdk/go/internal/hostcall (module calls out to the engine) both need —
// factored out on its own so hostcall doesn't have to import the whole
// sdk/go/engine package (which itself imports sdk/go/events, and would
// otherwise cycle back through sdk/go/db/sdk/go/events importing
// hostcall).
package wasmmem

import (
	"sync"
	"unsafe"
)

// live retains a real Go reference to every buffer Allocate hands out,
// keyed by the same address returned to the caller. Without this, the
// only thing surviving past Allocate's return is a bare uint32 — as far
// as the garbage collector is concerned that's just a number, not a
// pointer, so the backing array becomes collectible the instant Allocate
// returns. That's latent right up until something JIT-triggers a GC
// while the address is still needed but unread — which host-call
// round trips do reliably: the host writes its response by calling this
// same module's allocate export a second time *before* the caller ever
// reads the first buffer, and the resulting allocation can reclaim and
// reuse the still-needed address, corrupting whichever value is read
// back later. Confirmed via host.orm.unlink: the engine computed
// Deleted:true, but the module read back a `false` byte because ReadMem
// dereferenced an address the GC had already handed to a different
// allocation in between.
var (
	liveMu sync.Mutex
	live   = map[uint32][]byte{}
)

func Allocate(size uint32) uint32 {
	buf := make([]byte, size)
	ptr := uint32(uintptr(unsafe.Pointer(&buf[0])))

	liveMu.Lock()
	live[ptr] = buf
	liveMu.Unlock()

	return ptr
}

func Deallocate(ptr, size uint32) {
	liveMu.Lock()
	delete(live, ptr)
	liveMu.Unlock()
}

func ReadMem(ptr, size uint32) []byte {
	return unsafe.Slice((*byte)(unsafe.Pointer(uintptr(ptr))), size)
}

func WriteMem(ptr uint32, data []byte) {
	dst := unsafe.Slice((*byte)(unsafe.Pointer(uintptr(ptr))), len(data))
	copy(dst, data)
}
