// Command providerfixture is a real Go module compiled to wasip1 WASM for
// internal/engine/jobdispatch's provider-job tests: a
// connector-shaped handle_job that answers a synchronous
// host.jobs.dispatch_provider_sync caller through the real
// sdk/go/jobs.SetResult, rather than a hand-assembled bytecode stand-in.
// The payload's "mode" picks the behaviour under test.
//
// Must be built with:
//
//	GOOS=wasip1 GOARCH=wasm go build -buildmode=c-shared -o providerfixture.wasm .
package main

import (
	"github.com/djangbahevans/goerp/sdk/go/engine"
	"github.com/djangbahevans/goerp/sdk/go/jobs"
	"github.com/vmihailenco/msgpack/v5"
)

type chargePayload struct {
	Mode      string `msgpack:"mode"`
	Reference string `msgpack:"reference"`
}

type chargeResult struct {
	CheckoutURL string `msgpack:"checkout_url"`
}

// spins is written by the "hang" mode's loop so the compiler keeps it.
var spins uint64

//go:wasmexport handle_job
func handleJob(ptr, length uint32) uint32 {
	var p chargePayload
	if err := msgpack.Unmarshal(engine.ReadMem(ptr, length), &p); err != nil {
		return 2
	}

	switch p.Mode {
	case "result":
		if err := jobs.SetResult(chargeResult{CheckoutURL: "https://checkout.example/" + p.Reference}); err != nil {
			return 1
		}
		return 0
	case "fail":
		return 1
	case "hang":
		for {
			spins++
		}
	default:
		return 0
	}
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
