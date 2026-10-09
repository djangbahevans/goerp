package main

import (
	"github.com/djangbahevans/goerp/sdk/go/engine"
	"github.com/djangbahevans/goerp/sdk/go/model"
)

var schema = model.Schema{
	Types: []model.TypeDeclaration{{Name: "contact_kind", Values: []string{"person", "company"}}},
	Models: []*model.ModelDeclaration{
		model.Define("contact", model.Table("contacts")).
			WithStandardFields().
			Field("name", model.Text().Required()).
			Field("kind", model.Enum("contact_kind")).
			EnableOps(model.List, model.Get, model.Create, model.Update),
	},
}

//go:wasmexport get_routes
func getRoutes() uint64 { return engine.SerialiseRouteTable() }

//go:wasmexport get_model_declarations
func getModels() uint64 { return engine.WriteModels(schema) }

//go:wasmexport get_data_migrations
func getMigrations() uint64 { return engine.WriteDataMigrations(nil) }

//go:wasmexport allocate
func allocate(size uint32) uint32 { return engine.Allocate(size) }

//go:wasmexport deallocate
func deallocate(ptr, size uint32) { engine.Deallocate(ptr, size) }

func main() {}
