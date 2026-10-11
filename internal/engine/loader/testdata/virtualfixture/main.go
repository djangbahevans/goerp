// Command virtualfixture declares a virtual model with read and list backends.
package main

import (
	"github.com/djangbahevans/goerp/sdk/go/engine"
	"github.com/djangbahevans/goerp/sdk/go/model"
	"github.com/djangbahevans/goerp/sdk/go/orm"
)

type fixtureItem struct{}

func (fixtureItem) ResourceName() string { return "legacy.item" }

var schema = model.Schema{
	Models: []*model.ModelDeclaration{
		model.Define("legacy.item", model.Virtual()).
			Field("sku", model.Char()).
			EnableOps(model.Get, model.List),
	},
}

func init() {
	orm.RegisterVirtualBackend[fixtureItem](orm.VirtualBackend{
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
