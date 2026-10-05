package cache

import (
	"errors"

	abi "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/djangbahevans/goerp/sdk/go/engine"
	"github.com/vmihailenco/msgpack/v5"
)

// loaders maps a cache name to its attached loader, over msgpack bytes.
var loaders = map[string]func(args []byte) ([]byte, error){}

const (
	errCodeLoaderNotRegistered = "cache.loader_not_registered"
	errCodeLoaderFailed        = "cache.loader_failed"
	errCodeInvalidRequest      = "cache.invalid_request"
)

// DispatchLoader is what a module's handle_cache_loader export calls
// (manifest-spec.md §26): decode the incoming abi.CacheLoaderRequest, run the
// loader attached to the named cache and pack an abi.CacheLoaderResponse. A
// module that defines a loading cache exports it as
//
//	//go:wasmexport handle_cache_loader
//	func handleCacheLoader(ptr, length uint32) uint64 { return cache.DispatchLoader(ptr, length) }
func DispatchLoader(ptr, length uint32) uint64 {
	resp := runLoader(engine.ReadMem(ptr, length))

	data, err := msgpack.Marshal(resp)
	if err != nil {
		data, _ = msgpack.Marshal(abi.CacheLoaderResponse{Error: &abi.CacheLoaderError{Code: errCodeLoaderFailed, Message: err.Error()}})
	}
	out := engine.Allocate(uint32(len(data)))
	engine.WriteMem(out, data)
	return uint64(out)<<32 | uint64(len(data))
}

func runLoader(request []byte) abi.CacheLoaderResponse {
	var req abi.CacheLoaderRequest
	if err := msgpack.Unmarshal(request, &req); err != nil {
		return loaderError(errCodeInvalidRequest, err.Error())
	}

	load, ok := loaders[req.LoaderFnName]
	if !ok {
		return loaderError(errCodeLoaderNotRegistered, "no loader registered for cache "+req.LoaderFnName)
	}

	value, err := load(req.LoaderArgs)
	if err != nil {
		if hostErr, ok := errors.AsType[*abi.HostError](err); ok {
			return loaderError(hostErr.Code, hostErr.Message)
		}
		return loaderError(errCodeLoaderFailed, err.Error())
	}
	return abi.CacheLoaderResponse{Value: value}
}

func loaderError(code, message string) abi.CacheLoaderResponse {
	return abi.CacheLoaderResponse{Error: &abi.CacheLoaderError{Code: code, Message: message}}
}
