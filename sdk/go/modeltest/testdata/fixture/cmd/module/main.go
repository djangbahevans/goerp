// Command module is sdk/go/modeltest's own end-to-end fixture — a real
// module built on the actual SDK (model.Define, engine.GET/POST,
// db.Insert, events.EmitTx), compiled to wasip1 WASM by
// modeltest.NewHarness itself the same way `goerp module build` would,
// exercising the harness's real dispatch/schema-sync/event-capture paths
// against a real module rather than a hand-assembled stand-in.
package main

import (
	"github.com/djangbahevans/goerp/sdk/go/db"
	"github.com/djangbahevans/goerp/sdk/go/engine"
	"github.com/djangbahevans/goerp/sdk/go/events"
	"github.com/djangbahevans/goerp/sdk/go/model"
)

var schema = model.Schema{
	Models: []*model.ModelDeclaration{
		model.Define("widgets.widget", model.Label("Widget"), model.LabelPlural("Widgets"), model.Table("widgets")).
			WithStandardFields().
			Field("name", model.Text().Required()),
	},
}

type createWidgetBody struct {
	Name string `json:"name"`
}

type widgetRow struct {
	ID   string `db:"id"`
	Name string `db:"name"`
}

func init() {
	engine.GET("/ping", func(req *engine.Request) *engine.Response {
		return engine.OK(map[string]string{"status": "ok"})
	}, engine.Auth(engine.AuthNone))

	engine.GET("/", func(req *engine.Request) *engine.Response {
		result, err := db.QueryRaw(`SELECT id, name FROM widgets ORDER BY name`, nil)
		if err != nil {
			return &engine.Response{StatusCode: 500, Body: map[string]any{
				"error": map[string]any{"code": "widgets.query_failed", "message": err.Error()},
			}}
		}
		return engine.OK(map[string]any{"data": result.AsMaps()})
	})

	engine.POST("/", func(req *engine.Request) *engine.Response {
		var body createWidgetBody
		if err := req.ParseJSON(&body); err != nil {
			return &engine.Response{StatusCode: 400, Body: map[string]any{
				"error": map[string]any{"code": "widgets.invalid_body", "message": err.Error()},
			}}
		}

		tx, err := db.Begin()
		if err != nil {
			return &engine.Response{StatusCode: 500, Body: map[string]any{
				"error": map[string]any{"code": "widgets.begin_failed", "message": err.Error()},
			}}
		}

		row, err := tx.ExecReturning[widgetRow](`INSERT INTO widgets (tenant_id, name) VALUES ($1, $2)`, req.TenantID, body.Name)
		if err != nil {
			_ = tx.Rollback()
			return &engine.Response{StatusCode: 500, Body: map[string]any{
				"error": map[string]any{"code": "widgets.insert_failed", "message": err.Error()},
			}}
		}

		if _, err := events.EmitTx(tx, "widgets.widget.created", map[string]any{"widget_id": row.ID, "name": row.Name}); err != nil {
			_ = tx.Rollback()
			return &engine.Response{StatusCode: 500, Body: map[string]any{
				"error": map[string]any{"code": "widgets.emit_failed", "message": err.Error()},
			}}
		}

		if err := tx.Commit(); err != nil {
			return &engine.Response{StatusCode: 500, Body: map[string]any{
				"error": map[string]any{"code": "widgets.commit_failed", "message": err.Error()},
			}}
		}

		return engine.Created(map[string]any{"id": row.ID, "name": row.Name})
	})
}

//go:wasmexport handle_request
func handleRequest(ptr, length uint32) uint64 {
	return engine.DispatchRequest(ptr, length)
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
