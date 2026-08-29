// Command viewsfixture is a real Go module compiled to wasip1 WASM for
// internal/engine/loader's own tests (goerp#120) — it declares a Table
// model with EnableOps(List, Get, Create, Update), EnableViews(ListView,
// FormView), and .Nav("Sales", "Widgets", 10), exercising LoadModule's
// EnableViews/Nav merge into the module's own Manifest.Views/Navigation
// (route.SynthesizeViews).
//
// Must be built with:
//
//	GOOS=wasip1 GOARCH=wasm go build -buildmode=c-shared -o viewsfixture.wasm .
package main

import (
	"github.com/djangbahevans/goerp/sdk/go/engine"
	"github.com/djangbahevans/goerp/sdk/go/model"
)

var schema = model.Schema{
	Models: []*model.ModelDeclaration{
		model.Define("widget", model.Table("widgets")).
			WithStandardFields().
			Field("name", model.Char().Required().Primary()).
			EnableOps(model.List, model.Get, model.Create, model.Update).
			EnableViews(model.ListView, model.FormView).
			Nav("Sales", "Widgets", 10),
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
