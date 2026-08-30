// Command installfixture is a real Go module compiled to wasip1 WASM for
// internal/engine/moduleinstall's own tests — a minimal domain module
// with one owned model, exercising the full install path (compile,
// get_routes/get_model_declarations, schema sync) through a real
// wazero-loaded binary, the same convention
// internal/engine/loader/testdata/realfixture documents.
//
// Must be built with:
//
//	GOOS=wasip1 GOARCH=wasm go build -buildmode=c-shared -o installfixture.wasm .
package main

import (
	"github.com/djangbahevans/goerp/sdk/go/engine"
	"github.com/djangbahevans/goerp/sdk/go/model"
)

// variant is empty by default; worker_test.go's compileFixtureVariant
// sets it via `-ldflags "-X main.variant=..."` so otherwise-identical
// source produces content-distinct WASM binaries — needed by any test
// that must avoid two "different" test modules sharing one entry in
// wasm.Runtime's content-addressed compilation cache (see
// Runtime.CompileModule's own doc comment). It also qualifies the owned
// model's own name below: two concurrently-installing "modules" built
// from this same fixture source are still two real, distinct Go
// packages/manifests as far as the engine is concerned, but if both left
// the model name as the literal "widgets.widget" they'd collide on the
// exact same physical table name in a shared tenant schema — a
// same-tenant, concurrent-CREATE-TABLE race no properly-namespaced real
// module would ever hit, since real modules don't share model names.
// Leaving the model name unqualified when variant is empty keeps every
// test that doesn't care about this collision (most of them) asserting
// against the same "widgets_widget" table name it always has.
var variant string

func modelName() string {
	if variant == "" {
		return "widgets.widget"
	}
	return "widgets_" + variant + ".widget"
}

var schema = model.Schema{
	Models: []*model.ModelDeclaration{
		model.Define(modelName(), model.Label("Widget"+variant), model.LabelPlural("Widgets")).
			WithStandardFields().
			Field("name", model.Text().Required()),
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
