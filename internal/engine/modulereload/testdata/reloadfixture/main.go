// Command reloadfixture is a real Go module compiled to wasip1 WASM for
// internal/engine/modulereload's own tests — a minimal domain module with
// one owned model, exercising the full hot-reload leader path (compile,
// get_routes/get_model_declarations, downgrade pre-check, schema sync)
// through a real wazero-loaded binary, the same convention
// internal/engine/moduleinstall/testdata/installfixture documents.
//
// Must be built with:
//
//	GOOS=wasip1 GOARCH=wasm go build -buildmode=c-shared -o reloadfixture.wasm .
package main

import (
	"github.com/djangbahevans/goerp/sdk/go/engine"
	"github.com/djangbahevans/goerp/sdk/go/model"
)

// wide is empty by default; leader_test.go's compileFixture sets it via
// `-ldflags "-X main.wide=1"` to add a second, nullable column to the
// owned model — a safe AddColumn schema sync applies automatically (an
// added NOT NULL column with no default is instead classified as needing
// an explicit data migration, migration-guide.md §1, not something this
// package's own tests exercise). leader_test.go's own downgrade test
// forces the live column NOT NULL afterward with a direct ALTER COLUMN,
// to exercise CheckDowngrade's blocked path
// (docs/engine-internals.md §8 "Downgrade detection") against a live
// schema an older, narrow fixture can't safely downgrade to.
var wide string

var schema = model.Schema{
	Models: []*model.ModelDeclaration{
		func() *model.ModelDeclaration {
			m := model.Define("widgets.widget", model.Label("Widget"), model.LabelPlural("Widgets")).
				WithStandardFields().
				Field("name", model.Text().Required())
			if wide != "" {
				m = m.Field("extra", model.Text())
			}
			return m
		}(),
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
