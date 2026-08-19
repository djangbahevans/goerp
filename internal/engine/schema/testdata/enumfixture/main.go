// Command enumfixture is a real Go module compiled to wasip1 WASM for
// internal/engine/schema's own tests (goerp#199) — it declares an
// Enum-kind field alongside its matching model.EnumType, so
// get_model_declarations()'s actual wire-format output can be fed into
// engine.SchemaDiffEngine.Diff/Execute the same way loader.LoadModule
// would for a real module, proving the SDK and engine sides agree on the
// wire format together, not just each independently against hand-built
// Go structs (already covered by diff_test.go's
// TestDiffAndExecute_CreatesEnumTypeAndColumnTogether).
//
// Must be built with:
//
//	GOOS=wasip1 GOARCH=wasm go build -buildmode=c-shared -o enumfixture.wasm .
//
// See internal/engine/loader/testdata/realfixture/main.go (goerp#234) for
// why -buildmode=c-shared is required on wasip1.
package main

import (
	"github.com/djangbahevans/goerp/sdk/go/engine"
	"github.com/djangbahevans/goerp/sdk/go/model"
)

var schema = model.Schema{
	Types: []model.TypeDeclaration{
		model.EnumType("order_state_enum", "draft", "confirmed", "done", "cancelled"),
	},
	Models: []*model.ModelDeclaration{
		model.Define("sales.order", model.Table("sales_orders")).
			WithStandardFields().
			Field("state", model.Enum("order_state_enum").Required().Default("'draft'")),
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
