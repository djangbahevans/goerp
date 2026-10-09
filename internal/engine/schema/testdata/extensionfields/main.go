package main

import (
	"github.com/djangbahevans/goerp/sdk/go/engine"
	"github.com/djangbahevans/goerp/sdk/go/model"
	"github.com/djangbahevans/goerp/sdk/go/orm"
	"github.com/djangbahevans/goerp/sdk/go/perm"
)

var schema = model.Schema{
	Types: []model.TypeDeclaration{{Name: "industry_kind", Values: []string{"retail", "service"}}},
	Extensions: []model.ModelExtension{
		model.Extend("contacts.contact").
			Field("industry", model.Char(80).
				Access(model.AccessRead(perm.Ref("industry:contact:read")), model.AccessWrite(perm.Ref("industry:contact:write"))).
				OnDeniedRead(model.Omit)).
			Field("rating", model.Integer().Required().Default("0")).
			Field("category", model.Enum("industry_kind")).
			Field("segment", model.Selection("small", "large")).
			Field("peer_id", model.Many2One("contacts.contact")).
			Field("score", model.Integer().Computed("extension_score").Store(true).Depends("rating")),
		model.Extend("contacts.contact").
			Index("contacts_industry_idx", model.BTreeIndex("industry")).
			Index("contacts_name_idx", model.BTreeIndex("name")),
	},
}

func init() {
	orm.RegisterComputed("extension_score", func(ctx orm.ComputeContext, record map[string]any) (any, error) {
		return int64(42), nil
	})
}

//go:wasmexport handle_orm_compute
func compute(ptr, length uint32) uint64 { return orm.DispatchComputed(ptr, length) }

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
