// Package schema declares the module test fixture models.
package schema

import (
	"github.com/djangbahevans/goerp/sdk/go/model"
	"github.com/djangbahevans/goerp/sdk/go/perm"
)

var CardPhoneRead = perm.Define("widgets:card:phone_read", perm.Description("Read card phone numbers"))

var Schema = model.Schema{
	Models: []*model.ModelDeclaration{
		model.Define("widgets.widget", model.Label("Widget"), model.LabelPlural("Widgets"), model.Table("widgets")).
			WithStandardFields().
			Field("name", model.Text().Required()),
		model.Define("widgets.gadget").
			WithStandardFields().
			Field("name", model.Text().Required()).
			Field("display_name", model.Text()).
			Field("state", model.Selection("draft", "done").Default("'draft'").Workflow(
				model.Transition("draft", "done", "finish"))).
			EnableOps(model.List, model.Get, model.Create),
		model.Define("widgets.gizmo", model.LabelPlural("Gizmo Boxes")).
			WithStandardFields().
			EnableOps(model.List),
		model.Define("widgets.kind_probe", model.Table("widgets_kind_probes")).
			WithStandardFields().
			Field("decimal_field", model.Decimal(10, 2).Required()).
			Field("timestamp_field", model.TimestampTZ().Required()).
			Field("date_field", model.Date().Required()).
			Field("time_field", model.Time().Required()).
			Field("jsonb_field", model.JSONB().Required()).
			Field("bytea_field", model.Bytea().Required()).
			Field("integer_field", model.Integer().Required()).
			Field("float_field", model.Float().Required()).
			Field("optional_note", model.Text()).
			Field("created_by_gadget_id", model.Many2One("widgets.gadget")).
			Field("priority", model.Enum("kind_probe_priority_enum").Required()),
		model.Define("widgets.ticket").
			WithStandardFields().
			Field("number", model.Sequence("TK-{seq:04}")).
			EnableOps(model.Create, model.Get),
		model.Define("widgets.kind_matrix").
			WithStandardFields().
			Field("char_field", model.Char(20)).
			Field("text_field", model.Text()).
			Field("integer_field", model.Integer()).
			Field("bigint_field", model.BigInt()).
			Field("float_field", model.Float()).
			Field("decimal_field", model.Decimal(10, 2)).
			Field("boolean_field", model.Boolean()).
			Field("uuid_field", model.UUID()).
			Field("timestamptz_field", model.TimestampTZ()).
			Field("date_field", model.Date()).
			Field("time_field", model.Time()).
			Field("jsonb_field", model.JSONB()).
			Field("bytea_field", model.Bytea()).
			Field("selection_field", model.Selection("a", "b")).
			Field("enum_field", model.Enum("kind_probe_priority_enum")).
			Field("gadget_id", model.Many2One("widgets.gadget")).
			Field("sequence_field", model.Sequence("KM-{seq:04}")).
			Field("link_type", model.Selection("widgets.gadget")).
			Field("dynamic_link_field_id", model.DynamicLink("link_type")).
			EnableOps(model.Create, model.Get, model.List, model.Update),
		model.Define("widgets.card", model.Table("widgets_cards")).
			WithStandardFields().
			Field("name", model.Text().Required()).
			Field("phone", model.Char().Access(model.AccessRead(CardPhoneRead)).OnDeniedRead(model.Omit)),
		model.Define("widgets.attachment").
			WithStandardFields().
			Field("reference_type", model.Selection("widgets.widget", "widgets.gadget").Required()).
			Field("reference_id", model.DynamicLink("reference_type").Required()),
	},
	Types: []model.TypeDeclaration{
		model.EnumType("kind_probe_priority_enum", "low", "medium", "high"),
	},
}
