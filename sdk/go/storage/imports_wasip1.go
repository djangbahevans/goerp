//go:build wasip1

package storage

//go:wasmimport host.storage upload
func hostStorageUpload(ptr, size uint32) uint64

//go:wasmimport host.storage url
func hostStorageURL(ptr, size uint32) uint64

//go:wasmimport host.storage metadata
func hostStorageMetadata(ptr, size uint32) uint64

//go:wasmimport host.storage delete
func hostStorageDelete(ptr, size uint32) uint64
