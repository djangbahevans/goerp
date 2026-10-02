//go:build wasip1

package http

//go:wasmimport host.http fetch
func hostHTTPFetch(ptr, size uint32) uint64
