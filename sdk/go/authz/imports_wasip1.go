//go:build wasip1

package authz

//go:wasmimport host.authz check
func hostAuthzCheck(ptr, size uint32) uint64

//go:wasmimport host.authz require
func hostAuthzRequire(ptr, size uint32) uint64

//go:wasmimport host.authz row_filter
func hostAuthzRowFilter(ptr, size uint32) uint64

//go:wasmimport host.authz field_check
func hostAuthzFieldCheck(ptr, size uint32) uint64
