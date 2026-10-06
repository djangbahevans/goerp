// Command module is goerp codegen's example module: EnableOps CRUD, custom
// actions defined with engine.DefineAction and raw routes declared with
// engine.Body and engine.Returns (internal/codegen's tests build it and generate its
// client).
package main

import (
	"time"

	"github.com/djangbahevans/goerp/sdk/go/engine"
	"github.com/djangbahevans/goerp/sdk/go/model"
)

var schema = model.Schema{
	Types: []model.TypeDeclaration{
		model.EnumType("contact_tier", "bronze", "silver", "gold"),
	},
	Models: []*model.ModelDeclaration{
		model.Define("contacts.contact", model.Label("Contact"), model.LabelPlural("Contacts")).
			WithStandardFields().
			Field("type", model.Selection("person", "company").Required()).
			Field("status", model.Selection("lead", "customer").Required().Default("'lead'").
				Workflow(model.Transition("lead", "customer", "convert"))).
			Field("name", model.Text().Required()).
			Field("display_name", model.Text().Computed("compute_display_name").Store(false)).
			Field("email", model.Char(255)).
			Field("company_id", model.Many2One("contacts.company")).
			Field("tier", model.Enum("contact_tier")).
			Field("credit_limit", model.Decimal(12, 2)).
			Field("is_active", model.Boolean().Required().Default("true")).
			Field("tags", model.JSONB()).
			EnableOps(model.List, model.Get, model.Create, model.Update, model.Delete),
		model.Define("contacts.company", model.Label("Company"), model.LabelPlural("Companies")).
			WithStandardFields().
			Field("name", model.Text().Required()).
			Field("contacts", model.One2Many("contacts.contact", "company_id")).
			EnableOps(model.List, model.Get),
	},
}

type MergeContactsRequest struct {
	TargetID  string   `json:"target_id"`
	SourceIDs []string `json:"source_ids"`
}

// Contact names the model's own record type, so engine.Returns[Contact]
// reuses it.
type Contact struct {
	ID string `json:"id"`
}

type ImportRow struct {
	Name  string  `json:"name"`
	Email *string `json:"email"`
}

type ImportRequest struct {
	Rows    []ImportRow       `json:"rows"`
	DryRun  bool              `json:"dry_run,omitempty"`
	Mapping map[string]string `json:"mapping,omitempty"`
}

type ImportResult struct {
	Created  int       `json:"created"`
	Errors   []string  `json:"errors"`
	Finished time.Time `json:"finished"`
}

type CompanyInput struct {
	Name string `json:"name"`
}

type SearchHit struct {
	ID    string  `json:"id"`
	Score float64 `json:"score"`
}

func ok(*engine.Request) *engine.Response { return engine.OK(nil) }

type contactModel struct{}

func (contactModel) ResourceName() string { return "contacts.contact" }

type companyModel struct{}

func (companyModel) ResourceName() string { return "contacts.company" }

func merge(*engine.Request, MergeContactsRequest) *engine.Response { return engine.OK(nil) }
func archive(*engine.Request, engine.NoBody) *engine.Response      { return engine.OK(nil) }
func createCompany(*engine.Request, CompanyInput) *engine.Response { return engine.OK(nil) }

func init() {
	engine.HandleAction(engine.DefineAction[contactModel, MergeContactsRequest]("merge",
		engine.Scope(engine.CollectionAction), engine.Returns[Contact]()), merge)
	engine.HandleAction(engine.DefineAction[contactModel, engine.NoBody]("archive"), archive)
	// A reserved-name action on a model whose EnableOps doesn't cover it.
	engine.HandleAction(engine.DefineAction[companyModel, CompanyInput](engine.Create), createCompany)

	engine.GET("/by-email/{email}", ok, engine.Returns[Contact]())
	engine.GET("/export", ok)
	engine.DELETE("/cache", ok)
	engine.POST("/import", ok, engine.Body[ImportRequest](), engine.Returns[ImportResult]())
	engine.GET("/search", ok, engine.Model("contacts.contact", engine.List), engine.Returns[SearchHit]())
	engine.WS("/live", ok)
}

//go:wasmexport handle_request
func handleRequest(ptr, length uint32) uint64 { return engine.DispatchRequest(ptr, length) }

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
