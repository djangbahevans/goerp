//go:build !wasip1

// Non-wasip1 builds back the host.storage import with a panicking stub —
// see sdk/go/db/imports_stub.go's doc comment for why there's no
// meaningful mock here.
package storage

func hostStorageUpload(ptr, size uint32) uint64 {
	panic("sdk/go/storage: host.storage.upload is only available in a wasip1 build")
}
