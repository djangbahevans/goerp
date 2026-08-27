//go:build wasip1

package authz

//go:wasmimport host.authz field_check
func hostAuthzFieldCheck(ptr, size uint32) uint64
