// Command typedroutesfixture is a real Go module compiled to wasip1 WASM for
// internal/engine/loader's own tests. It declares a raw route and an
// engine.Action route with engine.Body/engine.Returns, and one route with
// neither, so a test can check the type descriptions survive get_routes
// into /_meta/schema.
//
// Must be built with:
//
//	GOOS=wasip1 GOARCH=wasm go build -buildmode=c-shared -o typedroutesfixture.wasm .
package main

import (
	"time"

	"github.com/djangbahevans/goerp/sdk/go/engine"
	"github.com/djangbahevans/goerp/sdk/go/model"
)

var schema = model.Schema{
	Models: []*model.ModelDeclaration{
		model.Define("contacts.contact", model.Label("Contact"), model.LabelPlural("Contacts")).
			WithStandardFields().
			Field("name", model.Text().Required()),
	},
}

type MergeContactsRequest struct {
	TargetID  string   `json:"target_id"`
	SourceIDs []string `json:"source_ids"`
}

type Contact struct {
	ID        string    `json:"id"`
	Email     *string   `json:"email"`
	Note      string    `json:"note,omitempty"`
	UpdatedAt time.Time `json:"updated_at"`
}

func init() {
	engine.POST("/export", func(req *engine.Request) *engine.Response {
		return engine.OK(nil)
	}, engine.Body[MergeContactsRequest](), engine.Returns[[]Contact]())

	engine.Action("contacts.contact", "merge", func(req *engine.Request) *engine.Response {
		return engine.OK(nil)
	}, engine.Scope(engine.CollectionAction), engine.Body[MergeContactsRequest](), engine.Returns[Contact]())

	engine.GET("/ping", func(req *engine.Request) *engine.Response {
		return engine.OK(nil)
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
