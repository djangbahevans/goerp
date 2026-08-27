//go:build wasip1

package storage

//go:wasmimport host.storage upload
func hostStorageUpload(ptr, size uint32) uint64
