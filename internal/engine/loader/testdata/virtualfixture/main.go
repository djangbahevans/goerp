// Command virtualfixture is a real Go module compiled to wasip1 WASM for
// internal/engine/loader's own tests — it declares a model.Virtual()
// model with EnableOps(Get, List) and a registered Read/List backend
// (goerp#345), no Create/Update/Delete, no ABAC condition. Used as both
// the "loads successfully under a connector manifest" and the "fails
// under a non-connector manifest" fixture — same binary, different
// manifest per test.
//
// Must be built with:
//
//	GOOS=wasip1 GOARCH=wasm go build -buildmode=c-shared -o virtualfixture.wasm .
package main

import (
	"github.com/djangbahevans/goerp/sdk/go/engine"
	"github.com/djangbahevans/goerp/sdk/go/model"
	"github.com/djangbahevans/goerp/sdk/go/orm"
)

var schema = model.Schema{
	Models: []*model.ModelDeclaration{
		model.Define("legacy.item", model.Virtual()).
			Field("sku", model.Char()).
			EnableOps(model.Get, model.List),
	},
}

func init() {
	orm.RegisterVirtualBackend("legacy.item", orm.VirtualBackend{
		Read: func(ctx orm.VirtualContext, id string) (map[string]any, error) {
			return map[string]any{"id": id}, nil
		},
		List: func(ctx orm.VirtualContext, params orm.VirtualListParams) ([]map[string]any, error) {
			return nil, nil
		},
	})
}

//go:wasmexport get_routes
func getRoutes() uint64 {
	return engine.SerialiseRouteTable()
}

//go:wasmexport get_model_declarations
func getModelDeclarations() uint64 {
	return engine.WriteModels(schema)
}

//go:wasmexport get_data_migrations
func getDataMigrations() uint64 {
	return engine.WriteDataMigrations(nil)
}

//go:wasmexport get_virtual_backends
func getVirtualBackends() uint64 {
	return orm.WriteVirtualBackendDescriptors()
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
