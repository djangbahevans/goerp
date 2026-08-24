// Command virtualfixture_listcondition is a real Go module compiled to
// wasip1 WASM for internal/engine/loader's own tests (goerp#345) — it
// declares EnableOps(List) with a per-op ABAC condition on a Virtual
// model, exercising the "row-filtered access to a Virtual model is
// Get-only" load-time rejection (go-sdk-reference.md §22).
//
// Must be built with:
//
//	GOOS=wasip1 GOARCH=wasm go build -buildmode=c-shared -o virtualfixture_listcondition.wasm .
package main

import (
	"github.com/djangbahevans/goerp/sdk/go/engine"
	"github.com/djangbahevans/goerp/sdk/go/model"
	"github.com/djangbahevans/goerp/sdk/go/orm"
)

var schema = model.Schema{
	Models: []*model.ModelDeclaration{
		model.Define("legacy.item3", model.Virtual()).
			Field("sku", model.Char()).
			EnableOps(model.Get, model.List.WithCondition("record.warehouse == current_user.warehouse")),
	},
}

func init() {
	orm.RegisterVirtualBackend("legacy.item3", orm.VirtualBackend{
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
