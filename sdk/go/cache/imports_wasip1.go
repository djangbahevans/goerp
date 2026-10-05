//go:build wasip1

package cache

//go:wasmimport host.cache get
func hostCacheGet(ptr, size uint32) uint64

//go:wasmimport host.cache set
func hostCacheSet(ptr, size uint32) uint64

//go:wasmimport host.cache delete
func hostCacheDelete(ptr, size uint32) uint64

//go:wasmimport host.cache invalidate_prefix
func hostCacheInvalidatePrefix(ptr, size uint32) uint64

//go:wasmimport host.cache get_or_set
func hostCacheGetOrSet(ptr, size uint32) uint64
