package main

import (
	"github.com/djangbahevans/goerp/sdk/go/engine"
	"github.com/djangbahevans/goerp/sdk/go/model"
	"github.com/djangbahevans/goerp/sdk/go/perm"
)

var registrationMode = "transition"

var confirmTransition = model.Transition("draft", "confirmed", "confirm").Requires(perm.Ref("sales:order:confirm"))

var schema = model.Schema{Models: []*model.ModelDeclaration{
	model.Define("order").WithStandardFields().
		Field("name", model.Text().Required()).
		Field("state", model.Selection("draft", "confirmed", "cancelled").Default("draft").Workflow(
			confirmTransition, model.Transition("confirmed", "cancelled", "cancel"),
		)),
}}

type order struct{}

func (order) ResourceName() string { return "sales.order" }

func confirmOrder(*engine.Request, engine.NoBody) *engine.Response {
	return &engine.Response{StatusCode: 202, Body: map[string]any{"handler": "confirmOrder"}}
}

func init() {
	switch registrationMode {
	case "transition":
		engine.HandleTransition[order](confirmTransition, confirmOrder)
	case "action":
		engine.HandleAction(engine.DefineAction[order, engine.NoBody]("confirm"), confirmOrder)
	case "mismatch":
		engine.HandleTransition[order](model.Transition("draft", "cancelled", "confirm"), confirmOrder)
	case "duplicate":
		engine.HandleTransition[order](confirmTransition, confirmOrder)
		engine.HandleTransition[order](confirmTransition, confirmOrder)
	}
}

//go:wasmexport handle_request
func handleRequest(ptr, length uint32) uint64 { return engine.DispatchRequest(ptr, length) }

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
