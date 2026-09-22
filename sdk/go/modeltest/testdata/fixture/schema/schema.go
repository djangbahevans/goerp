// Package schema is this fixture module's importable model.Schema
// declaration — the go-sdk-reference.md §22/goerp#958 convention a
// generator can build and run in isolation, without pulling in the rest
// of the module.
package schema

import "github.com/djangbahevans/goerp/sdk/go/model"

var Schema = model.Schema{
	Models: []*model.ModelDeclaration{
		model.Define("widgets.widget", model.Label("Widget"), model.LabelPlural("Widgets"), model.Table("widgets")).
			WithStandardFields().
			Field("name", model.Text().Required()),
		model.Define("widgets.gadget").
			WithStandardFields().
			Field("name", model.Text().Required()).
			Field("state", model.Selection("draft", "done").Default("'draft'").Workflow(
				model.Transition("draft", "done", "finish"))).
			EnableOps(model.List, model.Get, model.Create),
		model.Define("widgets.gizmo", model.LabelPlural("Gizmo Boxes")).
			WithStandardFields().
			EnableOps(model.List),
		// kind_probe exists solely to round-trip the six field kinds
		// goerp#960 empirically characterizes (Decimal, TimestampTZ,
		// Date, Time, JSONB, Bytea) through a real Postgres read —
		// nothing in the rest of this fixture exercises them.
		model.Define("widgets.kind_probe", model.Table("widgets_kind_probes")).
			WithStandardFields().
			Field("decimal_field", model.Decimal(10, 2).Required()).
			Field("timestamp_field", model.TimestampTZ().Required()).
			Field("date_field", model.Date().Required()).
			Field("time_field", model.Time().Required()).
			Field("jsonb_field", model.JSONB().Required()).
			Field("bytea_field", model.Bytea().Required()),
	},
}
