// Command groupedroutesfixture is a real Go module compiled to wasip1 WASM
// for internal/engine/loader's own tests. It registers routes on
// engine.Group, so a test can check the composed paths and the group's
// middleware reach the route declarations the engine loads.
//
// Must be built with:
//
//	GOOS=wasip1 GOARCH=wasm go build -buildmode=c-shared -o groupedroutesfixture.wasm .
package main

import (
	"time"

	"github.com/djangbahevans/goerp/sdk/go/engine"
	"github.com/djangbahevans/goerp/sdk/go/model"
	"github.com/djangbahevans/goerp/sdk/go/perm"
)

var (
	orderRead  = perm.Ref("sales:order:read")
	orderWrite = perm.Ref("sales:order:write")
)

var schema = model.Schema{}

func ok(*engine.Request) *engine.Response { return engine.OK(nil) }

func init() {
	orders := engine.Group("/orders",
		engine.RequirePermission(orderRead),
		engine.RateLimit(10, 60, engine.PerUser),
		engine.Timeout(5*time.Second),
	)
	orders.GET("", ok)
	orders.GET("/{id}", ok)
	orders.POST("", ok, engine.Requires(orderWrite), engine.MaxBody(2048))
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
