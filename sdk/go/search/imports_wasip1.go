//go:build wasip1

package search

//go:wasmimport host.search query
func hostSearchQuery(ptr, size uint32) uint64
