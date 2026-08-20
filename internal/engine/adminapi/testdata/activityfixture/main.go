// Command activityfixture is a real Go module compiled to wasip1 WASM for
// internal/engine/adminapi's activity-dispatch route tests — it registers a
// real activity via the SDK's engine.OnActivity and exports handle_activity
// via engine.DispatchActivity, the same way a real module would
// (go-sdk-reference.md §21a), rather than a hand-assembled bytecode
// stand-in.
//
// Must be built with:
//
//	GOOS=wasip1 GOARCH=wasm go build -buildmode=c-shared -o activityfixture.wasm .
package main

import "github.com/djangbahevans/goerp/sdk/go/engine"

type reserveInput struct {
	OrderID string `msgpack:"order_id"`
}

type reserveOutput struct {
	ReservationID string `msgpack:"reservation_id"`
}

func init() {
	engine.OnActivity("reserve_inventory", func(ctx *engine.ActivityContext, in reserveInput) (reserveOutput, error) {
		if in.OrderID == "" {
			return reserveOutput{}, engine.WorkflowApplicationError("invalid_order", map[string]any{"reason": "order_id is required"})
		}
		return reserveOutput{ReservationID: "res-" + in.OrderID}, nil
	})
}

//go:wasmexport handle_activity
func handleActivity(ptr, length uint32) uint64 {
	return engine.DispatchActivity(ptr, length)
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
