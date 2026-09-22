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
			// display_name has no other purpose than being the literal
			// column name host_orm_relations.go's expandRelations falls
			// back to for a Many2One target's expansion label
			// (internal/engine/wasm/host_orm_relations.go's own
			// displayNameField note) — kind_probe's created_by_gadget_id
			// below targets this model specifically so that expansion
			// has something real to resolve.
			Field("display_name", model.Text()).
			Field("state", model.Selection("draft", "done").Default("'draft'").Workflow(
				model.Transition("draft", "done", "finish"))).
			EnableOps(model.List, model.Get, model.Create),
		model.Define("widgets.gizmo", model.LabelPlural("Gizmo Boxes")).
			WithStandardFields().
			EnableOps(model.List),
		// kind_probe exists to round-trip every FieldKind goerp#961's
		// generated struct shape has to handle — the six kinds goerp#960
		// empirically characterizes (Decimal, TimestampTZ, Date, Time,
		// JSONB, Bytea), a nullable field (optional_note), a Many2One
		// relation (created_by_gadget_id), and an Enum field (priority).
		model.Define("widgets.kind_probe", model.Table("widgets_kind_probes")).
			WithStandardFields().
			Field("decimal_field", model.Decimal(10, 2).Required()).
			Field("timestamp_field", model.TimestampTZ().Required()).
			Field("date_field", model.Date().Required()).
			Field("time_field", model.Time().Required()).
			Field("jsonb_field", model.JSONB().Required()).
			Field("bytea_field", model.Bytea().Required()).
			Field("optional_note", model.Text()).
			Field("created_by_gadget_id", model.Many2One("widgets.gadget")).
			Field("priority", model.Enum("kind_probe_priority_enum").Required()),
		// attachment exercises DynamicLink generation end to end
		// (go-sdk-reference.md §22 "DynamicLink") — reference_id's
		// target varies per row via its sibling reference_type Selection
		// field, so goerp#961's generated struct has no expansion field
		// for it, just the two plain columns.
		model.Define("widgets.attachment").
			WithStandardFields().
			Field("reference_type", model.Selection("widgets.widget", "widgets.gadget").Required()).
			Field("reference_id", model.DynamicLink("reference_type").Required()),
	},
	Types: []model.TypeDeclaration{
		model.EnumType("kind_probe_priority_enum", "low", "medium", "high"),
	},
}
