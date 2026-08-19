// Command realfixture is a real Go module compiled to wasip1 WASM for
// internal/engine/loader's own tests — it exercises the SDK's actual
// get_routes/get_model_declarations/get_data_migrations wire-format output
// through a real wazero-loaded binary, rather than the package's other
// fixtures' hand-assembled bytecode with hardcoded empty results (goerp#234).
//
// Must be built with:
//
//	GOOS=wasip1 GOARCH=wasm go build -buildmode=c-shared -o realfixture.wasm .
//
// -buildmode=c-shared is required on wasip1 to produce a WASI reactor/
// library (exporting "_initialize") rather than the default WASI command
// mode (exporting "_start", which always calls proc_exit after main()
// returns — see `go help buildmode`).
package main

import (
	"github.com/djangbahevans/goerp/sdk/go/engine"
	"github.com/djangbahevans/goerp/sdk/go/model"
)

var schema = model.Schema{
	Models: []*model.ModelDeclaration{
		model.Define("widgets.widget", model.Label("Widget"), model.LabelPlural("Widgets")).
			WithStandardFields().
			Field("name", model.Text().Required()),
	},
}

var dataMigrations = []model.DataMigration{
	{FromVersion: "0.9.0", ToVersion: "1.0.0", Handler: "backfill_widget_name"},
}

func init() {
	engine.GET("/widgets/ping", func(req *engine.Request) *engine.Response {
		return engine.OK(map[string]string{"status": "ok"})
	})
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
	return engine.WriteDataMigrations(dataMigrations)
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
