// Command installfixture is a real Go module compiled to wasip1 WASM for
// internal/engine/moduleinstall's own tests — a minimal domain module
// with one owned model, exercising the full install path (compile,
// get_routes/get_model_declarations, schema sync) through a real
// wazero-loaded binary, the same convention
// internal/engine/loader/testdata/realfixture documents.
//
// Must be built with:
//
//	GOOS=wasip1 GOARCH=wasm go build -buildmode=c-shared -o installfixture.wasm .
package main

import (
	"github.com/djangbahevans/goerp/sdk/go/engine"
	"github.com/djangbahevans/goerp/sdk/go/model"
)

var schema = model.Schema{
	Models: []*model.ModelDeclaration{
		model.Define("widgets.widget", model.Label("Widget"), model.LabelPlural("Widgets")).
			WithStandardFields().
			Field("name", model.Text().Required()),
	},
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

//go:wasmexport allocate
func allocate(size uint32) uint32 {
	return engine.Allocate(size)
}

//go:wasmexport deallocate
func deallocate(ptr, size uint32) {
	engine.Deallocate(ptr, size)
}

func main() {}
