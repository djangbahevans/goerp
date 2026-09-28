//go:build wasip1

package config

//go:wasmimport host.config get
func hostConfigGet(ptr, size uint32) uint64

//go:wasmimport host.config set
func hostConfigSet(ptr, size uint32) uint64
