// Command transientfixture_listviolation is a real Go module compiled to
// wasip1 WASM for internal/engine/loader's own tests (goerp#344) — it
// declares EnableOps(List) on a Transient model, exercising the
// "Transient models have no browse semantics" load-time rejection
// (go-sdk-reference.md §22).
//
// Must be built with:
//
//	GOOS=wasip1 GOARCH=wasm go build -buildmode=c-shared -o transientfixture_listviolation.wasm .
package main

import (
	"time"

	"github.com/djangbahevans/goerp/sdk/go/engine"
	"github.com/djangbahevans/goerp/sdk/go/model"
)

var schema = model.Schema{
	Models: []*model.ModelDeclaration{
		model.Define("wizard.import_wizard", model.Transient(30*time.Minute)).
			Field("id", model.UUID().PrimaryKey()).
			Field("name", model.Text().Required()).
			EnableOps(model.Get, model.List),
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
