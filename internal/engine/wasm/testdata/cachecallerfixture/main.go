// Command cachecallerfixture is a real Go module compiled to wasip1 WASM for
// internal/engine/wasm's host.cache module-side test — it defines a loading
// cache through the real sdk/go/cache package and exports handle_cache_loader
// through cache.DispatchLoader, rather than a hand-assembled stand-in. Its
// loader returns the time it ran, so two equal values prove the second read
// was a hit.
//
// Must be built with:
//
//	GOOS=wasip1 GOARCH=wasm go build -buildmode=c-shared -o cachecallerfixture.wasm .
package main

import (
	"time"

	"github.com/djangbahevans/goerp/sdk/go/cache"
	"github.com/djangbahevans/goerp/sdk/go/engine"
	"github.com/vmihailenco/msgpack/v5"
)

type args struct {
	ID string `msgpack:"id"`
}

type loaded struct {
	ID       string `msgpack:"id"`
	LoadedAt int64  `msgpack:"loaded_at"`
}

type result struct {
	OK       bool   `msgpack:"ok"`
	Error    string `msgpack:"error,omitempty"`
	Found    bool   `msgpack:"found,omitempty"`
	LoadedAt int64  `msgpack:"loaded_at,omitempty"`
}

var contactCache = cache.Define[args, loaded]("contact",
	func(a args) string { return a.ID },
	cache.TTL(time.Minute),
).Loader(func(a args) (loaded, error) {
	return loaded{ID: a.ID, LoadedAt: time.Now().UnixNano()}, nil
})

func writeResult(r result) uint64 {
	data, err := msgpack.Marshal(r)
	if err != nil {
		data, _ = msgpack.Marshal(result{Error: "marshal result: " + err.Error()})
	}
	ptr := engine.Allocate(uint32(len(data)))
	engine.WriteMem(ptr, data)
	return uint64(ptr)<<32 | uint64(len(data))
}

//go:wasmexport run_get
func runGet() uint64 {
	v, err := contactCache.Get(args{ID: "c1"})
	if err != nil {
		return writeResult(result{Error: err.Error()})
	}
	return writeResult(result{OK: true, LoadedAt: v.LoadedAt})
}

//go:wasmexport run_lookup
func runLookup() uint64 {
	v, found, err := contactCache.Lookup(args{ID: "c1"})
	if err != nil {
		return writeResult(result{Error: err.Error()})
	}
	return writeResult(result{OK: true, Found: found, LoadedAt: v.LoadedAt})
}

//go:wasmexport run_invalidate_all
func runInvalidateAll() uint64 {
	if err := contactCache.InvalidateAll(); err != nil {
		return writeResult(result{Error: err.Error()})
	}
	return writeResult(result{OK: true})
}

//go:wasmexport handle_cache_loader
func handleCacheLoader(ptr, length uint32) uint64 {
	return cache.DispatchLoader(ptr, length)
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
