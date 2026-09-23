// Command module is sdk/go/modeltest's own end-to-end fixture — a real
// module built on the actual SDK (model.Define, engine.GET/POST,
// db.Insert, events.EmitTx), compiled to wasip1 WASM by
// modeltest.NewHarness itself the same way `goerp module build` would,
// exercising the harness's real dispatch/schema-sync/event-capture paths
// against a real module rather than a hand-assembled stand-in.
package main

import (
	"encoding/json"
	"time"

	"github.com/djangbahevans/goerp/sdk/go/db"
	"github.com/djangbahevans/goerp/sdk/go/engine"
	"github.com/djangbahevans/goerp/sdk/go/events"
	"github.com/djangbahevans/goerp/sdk/go/modeltest/testdata/fixture/models"
	"github.com/djangbahevans/goerp/sdk/go/modeltest/testdata/fixture/schema"
	"github.com/djangbahevans/goerp/sdk/go/orm"
)

type createWidgetBody struct {
	Name string `json:"name"`
}

// echoBody is /echo's request shape (goerp#948) — a nil Tags/Meta must
// arrive here as an empty slice/map, not nil, once modeltest's own
// request encoding moves off encoding/json (v1)'s null-for-nil default.
type echoBody struct {
	Tags []string          `json:"tags"`
	Meta map[string]string `json:"meta"`
}

func init() {
	engine.Action("widgets.gizmo", engine.List, func(req *engine.Request) *engine.Response {
		return engine.OK(map[string]string{"served_by": "module", "action": req.Action})
	})

	engine.Action("widgets.gizmo", "ship", func(req *engine.Request) *engine.Response {
		return engine.OK(map[string]string{"id": req.PathParams["id"], "model": req.Model, "action": req.Action})
	})

	engine.Action("widgets.gizmo", "restock", func(req *engine.Request) *engine.Response {
		return engine.OK(map[string]string{"action": req.Action})
	}, engine.Scope(engine.CollectionAction), engine.Method(engine.MethodPut))

	engine.POST("/kind-probe", func(req *engine.Request) *engine.Response {
		jsonbField, _ := json.Marshal(map[string]any{"key": "value", "n": float64(1)})

		vals := models.NewKindProbeValues().
			SetDecimalField("123.45").
			SetTimestampField(time.Date(2024, 3, 15, 10, 30, 0, 0, time.UTC)).
			SetDateField(time.Date(2024, 3, 15, 0, 0, 0, 0, time.UTC)).
			SetTimeField("13:45:00").
			SetJsonbField(jsonbField).
			SetByteaField([]byte("hello-bytes")).
			SetIntegerField(int32(42)).
			SetFloatField(3.5).
			SetPriority("medium")

		created, err := orm.Create[models.KindProbe](&vals.Values)
		if err != nil {
			return &engine.Response{StatusCode: 500, Body: map[string]any{
				"error": map[string]any{"code": "widgets.kind_probe_create_failed", "message": err.Error()},
			}}
		}

		read, err := orm.Get[models.KindProbe](created.ID)
		if err != nil {
			return &engine.Response{StatusCode: 500, Body: map[string]any{
				"error": map[string]any{"code": "widgets.kind_probe_read_failed", "message": err.Error()},
			}}
		}

		return engine.Created(map[string]any{
			"id":                   read.ID,
			"decimal":              read.DecimalField,
			"timestamp":            read.TimestampField.Format(time.RFC3339),
			"date":                 read.DateField.Format(time.RFC3339),
			"time":                 read.TimeField,
			"jsonb":                string(read.JsonbField),
			"bytea":                string(read.ByteaField),
			"integer":              read.IntegerField,
			"float":                read.FloatField,
			"priority":             string(read.Priority),
			"has_note":             read.OptionalNote != nil,
			"has_gadget_expansion": read.CreatedByGadget.RelationRef != nil,
		})
	}, engine.Auth(engine.AuthNone))

	// /kind-probe-relation exercises the generated orm.Ref[Gadget]
	// expansion field (goerp#961 §3.4, goerp#979) end to end: a gadget created with
	// a real display_name, a kind_probe row whose Many2One FK points at
	// it, read back via orm.Query's All (not just Get, per goerp#961's
	// own AC) — and, in the same call, a second kind_probe row with the
	// relation left unset, to confirm the expansion field decodes to nil
	// rather than erroring when there's nothing to expand.
	engine.POST("/kind-probe-relation", func(req *engine.Request) *engine.Response {
		gadgetVals := models.NewGadgetValues().
			SetName("Acme Gadget").
			SetDisplayName("Acme")

		gadget, err := orm.Create[models.Gadget](&gadgetVals.Values)
		if err != nil {
			return &engine.Response{StatusCode: 500, Body: map[string]any{
				"error": map[string]any{"code": "widgets.gadget_create_failed", "message": err.Error()},
			}}
		}

		emptyJSONObject, _ := json.Marshal(map[string]any{})

		// setBaseKindProbeVals fills the fields common to both rows below —
		// with/withoutRelation only differ in created_by_gadget_id.
		setBaseKindProbeVals := func(v *models.KindProbeValues) *models.KindProbeValues {
			return v.
				SetDecimalField("1.00").
				SetTimestampField(time.Now().UTC()).
				SetDateField(time.Now().UTC()).
				SetTimeField("00:00:00").
				SetJsonbField(emptyJSONObject).
				SetByteaField([]byte("")).
				SetIntegerField(int32(0)).
				SetFloatField(0.0).
				SetPriority("low")
		}

		withRelationVals := setBaseKindProbeVals(models.NewKindProbeValues().SetCreatedByGadgetID(gadget.ID))
		withRelation, err := orm.Create[models.KindProbe](&withRelationVals.Values)
		if err != nil {
			return &engine.Response{StatusCode: 500, Body: map[string]any{
				"error": map[string]any{"code": "widgets.kind_probe_create_failed", "message": err.Error()},
			}}
		}

		withoutRelationVals := setBaseKindProbeVals(models.NewKindProbeValues())
		withoutRelation, err := orm.Create[models.KindProbe](&withoutRelationVals.Values)
		if err != nil {
			return &engine.Response{StatusCode: 500, Body: map[string]any{
				"error": map[string]any{"code": "widgets.kind_probe_create_failed", "message": err.Error()},
			}}
		}

		rows, _, err := orm.From[models.KindProbe]().
			Where(models.KindProbeFields.ID.In(withRelation.ID, withoutRelation.ID)).
			All()
		if err != nil {
			return &engine.Response{StatusCode: 500, Body: map[string]any{
				"error": map[string]any{"code": "widgets.kind_probe_search_failed", "message": err.Error()},
			}}
		}

		body := map[string]any{}
		for _, row := range rows {
			entry := map[string]any{"gadget_id": row.CreatedByGadgetID}
			if row.CreatedByGadget.RelationRef != nil {
				entry["gadget_display_name"] = row.CreatedByGadget.DisplayName
			} else {
				entry["gadget_display_name"] = nil
			}
			switch row.ID {
			case withRelation.ID:
				body["with_relation"] = entry
			case withoutRelation.ID:
				body["without_relation"] = entry
			}
		}

		return engine.Created(body)
	}, engine.Auth(engine.AuthNone))

	// /gadget-query-delete exercises goerp#980's generated instance
	// convenience methods end to end: Gadget{}.Query() must produce
	// identical results to orm.From[models.Gadget]() (the AC's own
	// example), and Delete() must actually soft-delete the record via
	// orm.Unlink — Gadget has a deleted_at column (WithStandardFields), so
	// Unlink sets it rather than removing the row, and a plain query still
	// returns it (host.orm applies no default deleted_at IS NULL filter).
	engine.POST("/gadget-query-delete", func(req *engine.Request) *engine.Response {
		vals := models.NewGadgetValues().
			SetName("QueryDeleteGadget").
			SetDisplayName("QueryDeleteGadget")

		gadget, err := orm.Create[models.Gadget](&vals.Values)
		if err != nil {
			return &engine.Response{StatusCode: 500, Body: map[string]any{
				"error": map[string]any{"code": "widgets.gadget_create_failed", "message": err.Error()},
			}}
		}

		viaMethod, _, err := models.Gadget{}.Query().Where(models.GadgetFields.Name.Eq("QueryDeleteGadget")).All()
		if err != nil {
			return &engine.Response{StatusCode: 500, Body: map[string]any{
				"error": map[string]any{"code": "widgets.gadget_query_failed", "message": err.Error()},
			}}
		}
		viaFrom, _, err := orm.From[models.Gadget]().Where(models.GadgetFields.Name.Eq("QueryDeleteGadget")).All()
		if err != nil {
			return &engine.Response{StatusCode: 500, Body: map[string]any{
				"error": map[string]any{"code": "widgets.gadget_query_failed", "message": err.Error()},
			}}
		}

		if _, err := gadget.Delete(); err != nil {
			return &engine.Response{StatusCode: 500, Body: map[string]any{
				"error": map[string]any{"code": "widgets.gadget_delete_failed", "message": err.Error()},
			}}
		}
		afterDelete, err := orm.Get[models.Gadget](gadget.ID)
		if err != nil {
			return &engine.Response{StatusCode: 500, Body: map[string]any{
				"error": map[string]any{"code": "widgets.gadget_get_failed", "message": err.Error()},
			}}
		}

		return engine.Created(map[string]any{
			"query_method_ids": idsOf(viaMethod),
			"from_func_ids":    idsOf(viaFrom),
			"soft_deleted":     afterDelete.DeletedAt != nil,
		})
	}, engine.Auth(engine.AuthNone))

	engine.GET("/ping", func(req *engine.Request) *engine.Response {
		return engine.OK(map[string]string{"status": "ok"})
	}, engine.Auth(engine.AuthNone))

	engine.POST("/echo", func(req *engine.Request) *engine.Response {
		var body echoBody
		if err := req.ParseJSON(&body); err != nil {
			return &engine.Response{StatusCode: 400, Body: map[string]any{
				"error": map[string]any{"code": "widgets.invalid_body", "message": err.Error()},
			}}
		}
		return engine.OK(map[string]any{"tags_is_nil": body.Tags == nil, "meta_is_nil": body.Meta == nil})
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

		row, err := tx.ExecReturning[models.Widget](`INSERT INTO widgets (tenant_id, name) VALUES ($1, $2)`, req.TenantID, body.Name)
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

// idsOf collects the ID of each gadget in gadgets, for comparing
// Gadget{}.Query()'s result against orm.From[models.Gadget]()'s own.
func idsOf(gadgets []models.Gadget) []string {
	ids := make([]string, len(gadgets))
	for i, g := range gadgets {
		ids[i] = g.ID
	}
	return ids
}

func main() {}
