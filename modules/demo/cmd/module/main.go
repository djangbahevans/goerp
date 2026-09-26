package main

import (
	"github.com/djangbahevans/goerp/modules/demo/schema"
	"github.com/djangbahevans/goerp/sdk/go/engine"
)

//go:wasmexport handle_request
func handleRequest(ptr, length uint32) uint64 {
	return engine.DispatchRequest(ptr, length)
}

//go:wasmexport handle_event
func handleEvent(ptr, length uint32) uint32 {
	return engine.DispatchEvent(ptr, length)
}

//go:wasmexport get_routes
func getRoutes() uint64 {
	return engine.SerialiseRouteTable()
}

//go:wasmexport get_model_declarations
func getModelDeclarations() uint64 {
	return engine.WriteModels(schema.Schema)
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
