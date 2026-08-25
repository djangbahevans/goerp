// Command virtualopfixture is a real Go module compiled to wasip1 WASM for
// internal/engine/wasm's own InvokeHandleVirtualOp tests — it registers a
// real Virtual backend via the SDK's orm.RegisterVirtualBackend and exports
// handle_virtual_op via orm.DispatchVirtualOp, the same way a real module
// would (go-sdk-reference.md §22 "Virtual models"), rather than a
// hand-assembled bytecode stand-in.
//
// Must be built with:
//
//	GOOS=wasip1 GOARCH=wasm go build -buildmode=c-shared -o virtualopfixture.wasm .
package main

import (
	"github.com/djangbahevans/goerp/sdk/go/engine"
	"github.com/djangbahevans/goerp/sdk/go/orm"
)

func init() {
	orm.RegisterVirtualBackend("legacy.item", orm.VirtualBackend{
		Read: func(ctx orm.VirtualContext, id string) (map[string]any, error) {
			return map[string]any{"id": id, "tenant_id": ctx.TenantID}, nil
		},
	})
}

//go:wasmexport handle_virtual_op
func handleVirtualOp(ptr, length uint32) uint64 {
	return orm.DispatchVirtualOp(ptr, length)
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
