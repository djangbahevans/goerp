// Package schema declares the async probe's handler effects.
package schema

import (
	"github.com/djangbahevans/goerp/sdk/go/model"
	_ "github.com/djangbahevans/goerp/sdk/go/modeltest/testdata/asyncfixture"
)

var Schema = model.Schema{
	Models: []*model.ModelDeclaration{
		model.Define("asyncprobe.effect", model.Table("effects")).WithStandardFields().
			Field("reference", model.Text()).
			Field("source", model.Text()).
			Field("country", model.Text()).
			Field("event_id", model.Text()).
			Field("tenant_seen", model.Text()).
			Field("user_seen", model.Text()).
			Field("type_seen", model.Text()).
			Field("attempt", model.Integer()).
			Field("roles_seen", model.Text()).
			Field("contact_seen", model.Text()),
	},
}
