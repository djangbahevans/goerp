// Command virtualopfixture exercises a virtual read backend through the SDK
// dispatch export in a real WASM module.
package main

import (
	"github.com/djangbahevans/goerp/sdk/go/engine"
	"github.com/djangbahevans/goerp/sdk/go/orm"
)

type fixtureItem struct{}

func (fixtureItem) ResourceName() string { return "legacy.item" }

func init() {
	orm.RegisterVirtualBackend[fixtureItem](orm.VirtualBackend{
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
