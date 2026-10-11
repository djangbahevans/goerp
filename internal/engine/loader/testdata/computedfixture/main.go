// Command computedfixture exposes configurable model dependencies for loader tests.
package main

import (
	"github.com/djangbahevans/goerp/sdk/go/engine"
	"github.com/djangbahevans/goerp/sdk/go/model"
)

var dependency = "name"
var sequenceFormat = "W/{seq:05}"
var routeMode string

func init() {
	if routeMode == "duplicate" {
		handler := func(*engine.Request) *engine.Response { return engine.OK(nil) }
		engine.GET("/same", handler)
		engine.GET("/same", handler)
	}
}

//go:wasmexport get_routes
func getRoutes() uint64 { return engine.SerialiseRouteTable() }

//go:wasmexport get_model_declarations
func getModelDeclarations() uint64 {
	return engine.WriteModels(model.Schema{Models: []*model.ModelDeclaration{
		model.Define("widgets.widget").WithStandardFields().
			Field("name", model.Char()).
			Field("reference", model.Sequence(sequenceFormat)).
			Field("parent_id", model.Many2One("widgets.widget")).
			Field("display", model.Char().Computed("_compute_display").Store(true).Depends(dependency)),
	}})
}

//go:wasmexport get_data_migrations
func getDataMigrations() uint64 { return engine.WriteDataMigrations(nil) }

//go:wasmexport allocate
func allocate(size uint32) uint32 { return engine.Allocate(size) }

//go:wasmexport deallocate
func deallocate(ptr, size uint32) { engine.Deallocate(ptr, size) }

func main() {}
