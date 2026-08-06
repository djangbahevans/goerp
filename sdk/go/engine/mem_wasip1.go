//go:build wasip1

package engine

import "unsafe"

func allocate(size uint32) uint32 {
	buf := make([]byte, size)
	return uint32(uintptr(unsafe.Pointer(&buf[0])))
}

func deallocate(ptr, size uint32) {
	_ = unsafe.Slice((*byte)(unsafe.Pointer(uintptr(ptr))), size)
}

func readMem(ptr, size uint32) []byte {
	return unsafe.Slice((*byte)(unsafe.Pointer(uintptr(ptr))), size)
}

func writeMem(ptr uint32, data []byte) {
	dst := unsafe.Slice((*byte)(unsafe.Pointer(uintptr(ptr))), len(data))
	copy(dst, data)
}
