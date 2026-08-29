// Command viewsfixture_listviolation is a real Go module compiled to
// wasip1 WASM for internal/engine/loader's own tests (goerp#120) — it
// declares EnableViews(ListView) without List in EnableOps, exercising
// LoadModule's failure path when route.SynthesizeViews rejects the
// declaration (go-sdk-reference.md §22 "EnableViews").
//
// Must be built with:
//
//	GOOS=wasip1 GOARCH=wasm go build -buildmode=c-shared -o viewsfixture_listviolation.wasm .
package main

import (
	"github.com/djangbahevans/goerp/sdk/go/engine"
	"github.com/djangbahevans/goerp/sdk/go/model"
)

var schema = model.Schema{
	Models: []*model.ModelDeclaration{
		model.Define("widget", model.Table("widgets")).
			WithStandardFields().
			Field("name", model.Char().Required()).
			EnableOps(model.Get).
			EnableViews(model.ListView),
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
