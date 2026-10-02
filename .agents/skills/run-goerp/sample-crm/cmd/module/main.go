// Command module is a minimal module with one model, a list view, a form view
// and a sidebar entry — enough to drive the shell's generic renderers.
package main

import (
	"github.com/djangbahevans/goerp/sdk/go/engine"
	"github.com/djangbahevans/goerp/sdk/go/model"
)

var schema = model.Schema{
	Models: []*model.ModelDeclaration{
		model.Define("crm.contact", model.Label("Contact"), model.LabelPlural("Contacts"), model.Table("contact")).
			WithStandardFields().
			Field("name", model.Text().Required()).
			Field("email", model.Text()).
			EnableOps(model.List, model.Get, model.Create, model.Update).
			EnableViews(model.ListView, model.FormView).
			Nav("CRM", "Contacts", 10),
	},
}

//go:wasmexport handle_request
func handleRequest(ptr, length uint32) uint64 { return engine.DispatchRequest(ptr, length) }

//go:wasmexport handle_event
func handleEvent(ptr, length uint32) uint32 { return engine.DispatchEvent(ptr, length) }

//go:wasmexport get_routes
func getRoutes() uint64 { return engine.SerialiseRouteTable() }

//go:wasmexport get_model_declarations
func getModelDeclarations() uint64 { return engine.WriteModels(schema) }

//go:wasmexport get_data_migrations
func getDataMigrations() uint64 { return engine.WriteDataMigrations(nil) }

//go:wasmexport allocate
func allocate(size uint32) uint32 { return engine.Allocate(size) }

//go:wasmexport deallocate
func deallocate(ptr, size uint32) { engine.Deallocate(ptr, size) }

func main() {}
