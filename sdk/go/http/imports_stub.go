//go:build !wasip1

package http

func hostHTTPFetch(ptr, size uint32) uint64 {
	panic("sdk/go/http: host.http.fetch is only available in a wasip1 build")
}
