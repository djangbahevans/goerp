//go:build !wasip1

// Non-wasip1 builds back the host.search import with a panicking stub —
// see sdk/go/db/imports_stub.go's doc comment for why there's no
// meaningful mock here.
package search

func hostSearchQuery(ptr, size uint32) uint64 {
	panic("sdk/go/search: host.search.query is only available in a wasip1 build")
}
