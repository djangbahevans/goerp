//go:build !wasip1

// Non-wasip1 builds back the host.cache imports with panicking stubs — see
// sdk/go/db/imports_stub.go's doc comment for why there's no meaningful mock
// here. Tests substitute the package's host seam instead.
package cache

func hostCacheGet(ptr, size uint32) uint64 {
	panic("sdk/go/cache: host.cache.get is only available in a wasip1 build")
}

func hostCacheSet(ptr, size uint32) uint64 {
	panic("sdk/go/cache: host.cache.set is only available in a wasip1 build")
}

func hostCacheDelete(ptr, size uint32) uint64 {
	panic("sdk/go/cache: host.cache.delete is only available in a wasip1 build")
}

func hostCacheInvalidatePrefix(ptr, size uint32) uint64 {
	panic("sdk/go/cache: host.cache.invalidate_prefix is only available in a wasip1 build")
}

func hostCacheGetOrSet(ptr, size uint32) uint64 {
	panic("sdk/go/cache: host.cache.get_or_set is only available in a wasip1 build")
}
