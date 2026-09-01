// Command searchcallerfixture is a real Go module compiled to wasip1
// WASM for internal/engine/wasm's own host.search module-side caller
// test (goerp#419) — it calls host.search.query through the real
// sdk/go/search package, against a "testmodule.widgets" search index the
// test harness declares and populates, rather than a hand-assembled
// bytecode stand-in.
//
// Must be built with:
//
//	GOOS=wasip1 GOARCH=wasm go build -buildmode=c-shared -o searchcallerfixture.wasm .
package main

import (
	"github.com/djangbahevans/goerp/sdk/go/engine"
	"github.com/djangbahevans/goerp/sdk/go/search"
	"github.com/vmihailenco/msgpack/v5"
)

type result struct {
	OK        bool     `msgpack:"ok"`
	Error     string   `msgpack:"error,omitempty"`
	Names     []string `msgpack:"names,omitempty"`
	TotalHits int64    `msgpack:"total_hits,omitempty"`
}

// widgetHit mirrors "widgets" search index's own Displayed columns
// (host_search_test.go's widgetSearchIndex) via json tags, the shape
// search.Query[T] populates each hit into.
type widgetHit struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

func writeResult(r result) uint64 {
	data, err := msgpack.Marshal(r)
	if err != nil {
		data, _ = msgpack.Marshal(result{Error: "marshal result: " + err.Error()})
	}
	ptr := engine.Allocate(uint32(len(data)))
	engine.WriteMem(ptr, data)
	return uint64(ptr)<<32 | uint64(len(data))
}

//go:wasmexport run_search_query
func runSearchQuery() uint64 {
	out, err := search.Query[widgetHit]("widgets", "Widget")
	if err != nil {
		return writeResult(result{Error: err.Error()})
	}
	names := make([]string, 0, len(out.Hits))
	for _, hit := range out.Hits {
		names = append(names, hit.Name)
	}
	return writeResult(result{OK: true, Names: names, TotalHits: int64(out.TotalHits)})
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
