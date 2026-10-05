// Command cacheloaderfixture is a Go module compiled to wasip1 WASM for
// internal/engine/wasm's host.cache.get_or_set tests. Its handle_cache_loader
// export implements two loaders by name: "upper" returns its arguments
// upper-cased and "fail" returns a loader error.
//
// Must be built with:
//
//	GOOS=wasip1 GOARCH=wasm go build -buildmode=c-shared -o cacheloaderfixture.wasm .
package main

import (
	"bytes"

	abi "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/djangbahevans/goerp/sdk/go/engine"
	"github.com/vmihailenco/msgpack/v5"
)

//go:wasmexport handle_cache_loader
func handleCacheLoader(ptr, length uint32) uint64 {
	var req abi.CacheLoaderRequest
	if err := msgpack.Unmarshal(engine.ReadMem(ptr, length), &req); err != nil {
		return respond(abi.CacheLoaderResponse{Error: &abi.CacheLoaderError{Code: "cache.invalid_request", Message: err.Error()}})
	}

	switch req.LoaderFnName {
	case "upper":
		return respond(abi.CacheLoaderResponse{Value: bytes.ToUpper(req.LoaderArgs)})
	case "fail":
		return respond(abi.CacheLoaderResponse{Error: &abi.CacheLoaderError{Code: "contacts.load_failed", Message: "loader failed"}})
	default:
		return respond(abi.CacheLoaderResponse{Error: &abi.CacheLoaderError{Code: "cache.loader_not_registered", Message: req.LoaderFnName}})
	}
}

func respond(resp abi.CacheLoaderResponse) uint64 {
	data, _ := msgpack.Marshal(resp)
	ptr := engine.Allocate(uint32(len(data)))
	engine.WriteMem(ptr, data)
	return uint64(ptr)<<32 | uint64(len(data))
}

//go:wasmexport allocate
func allocate(size uint32) uint32 {
	return engine.Allocate(size)
}

//go:wasmexport deallocate
func deallocate(ptr, size uint32) {
	engine.Deallocate(ptr, size)
}

func main() {}
