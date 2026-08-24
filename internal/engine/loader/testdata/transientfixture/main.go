// Command transientfixture is a real Go module compiled to wasip1 WASM
// for internal/engine/loader's own tests — it declares a
// model.Transient() model with EnableOps(Get, Create, Update, Delete)
// (goerp#344), no List, a positive TTL. Used for the "loads
// successfully" fixture.
//
// Must be built with:
//
//	GOOS=wasip1 GOARCH=wasm go build -buildmode=c-shared -o transientfixture.wasm .
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
			EnableOps(model.Get, model.Create, model.Update, model.Delete),
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
