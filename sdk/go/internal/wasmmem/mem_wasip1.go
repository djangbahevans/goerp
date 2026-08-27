//go:build wasip1

// Package wasmmem is the module-side linear-memory allocator/accessor
// pair sdk/go/engine (module exports the engine calls into) and
// sdk/go/internal/hostcall (module calls out to the engine) both need —
// factored out on its own so hostcall doesn't have to import the whole
// sdk/go/engine package (which itself imports sdk/go/events, and would
// otherwise cycle back through sdk/go/db/sdk/go/events importing
// hostcall).
package wasmmem

import "unsafe"

func Allocate(size uint32) uint32 {
	buf := make([]byte, size)
	return uint32(uintptr(unsafe.Pointer(&buf[0])))
}

func Deallocate(ptr, size uint32) {
	_ = unsafe.Slice((*byte)(unsafe.Pointer(uintptr(ptr))), size)
}

func ReadMem(ptr, size uint32) []byte {
	return unsafe.Slice((*byte)(unsafe.Pointer(uintptr(ptr))), size)
}

func WriteMem(ptr uint32, data []byte) {
	dst := unsafe.Slice((*byte)(unsafe.Pointer(uintptr(ptr))), len(data))
	copy(dst, data)
}
