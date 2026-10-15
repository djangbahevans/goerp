//go:build wasip1

package ui

//go:wasmimport host.ui push
func hostUIPush(ptr, size uint32) uint64
