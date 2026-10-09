package schema

import "github.com/djangbahevans/goerp/sdk/go/model"

var Schema = model.Schema{
	Models: []*model.ModelDeclaration{
		model.Define("demo.item", model.Table("items"), model.Label("Item"), model.LabelPlural("Items")).
			WithStandardFields().
			Field("name", model.Text().Required().Primary()).
			EnableOps(model.List, model.Get, model.Create, model.Update).
			EnableViews(model.ListView, model.FormView).
			Nav("Demo", "Items", 10),
	},
}
