//go:build !wasip1

package storage

func hostStorageUpload(ptr, size uint32) uint64 {
	panic("sdk/go/storage: host.storage.upload is only available in a wasip1 build")
}

func hostStorageURL(ptr, size uint32) uint64 {
	panic("sdk/go/storage: host.storage.url is only available in a wasip1 build")
}

func hostStorageMetadata(ptr, size uint32) uint64 {
	panic("sdk/go/storage: host.storage.metadata is only available in a wasip1 build")
}

func hostStorageDelete(ptr, size uint32) uint64 {
	panic("sdk/go/storage: host.storage.delete is only available in a wasip1 build")
}
