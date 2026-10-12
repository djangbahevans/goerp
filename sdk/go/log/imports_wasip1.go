//go:build wasip1

package log

//go:wasmimport host.log log
func hostLog(ptr, size uint32) uint64
