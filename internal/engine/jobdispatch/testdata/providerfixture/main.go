// Command providerfixture is a real Go module compiled to wasip1 WASM for
// internal/engine/jobdispatch's provider-job tests: a
// connector-shaped payment_charge handler, registered with
// engine.HandleProviderJob, that answers a synchronous host.jobs.dispatch_provider_sync caller
// through the real sdk/go/jobs.SetResult, rather than a hand-assembled
// bytecode stand-in. The payload's "mode" picks the behaviour under test.
//
// Must be built with:
//
//	GOOS=wasip1 GOARCH=wasm go build -buildmode=c-shared -o providerfixture.wasm .
package main

import (
	"errors"

	"github.com/djangbahevans/goerp/sdk/go/engine"
	"github.com/djangbahevans/goerp/sdk/go/jobs"
)

type chargePayload struct {
	Mode      string `msgpack:"mode"`
	Reference string `msgpack:"reference"`
}

type chargeResult struct {
	CheckoutURL string `msgpack:"checkout_url"`
}

var chargeJob = jobs.DefineProvider[chargePayload, chargeResult]("payment_provider", "payment_charge")

// spins is written by the "hang" mode's loop so the compiler keeps it.
var spins uint64

func init() {
	engine.HandleProviderJob(chargeJob, func(_ *engine.JobContext, p chargePayload) error {
		switch p.Mode {
		case "result":
			return jobs.SetResult(chargeJob, chargeResult{CheckoutURL: "https://checkout.example/" + p.Reference})
		case "fail":
			return errors.New("intentional failure for testing")
		case "hang":
			for {
				spins++
			}
		}
		return nil
	})
}

//go:wasmexport handle_job
func handleJob(ptr, length uint32) uint32 {
	return engine.DispatchJob(ptr, length)
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
