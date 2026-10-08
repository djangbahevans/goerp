package schema

import "github.com/djangbahevans/goerp/sdk/go/model"

var Schema = model.Schema{
	Models: []*model.ModelDeclaration{
		model.Define("contacts.contact",
			model.Table("contacts"),
			model.Label("Contact"),
			model.LabelPlural("Contacts"),
		).
			WithStandardFields().
			Field("type", model.Selection("person", "company").Required().Default("'company'")).
			Field("name", model.Char().Required()).
			Field("display_name", model.Char().Computed("_compute_display_name").Store(true).Depends("name", "type", "company_id", "company.name")).
			Field("company_id", model.Many2One("contacts.contact").
				Domain("type = 'company'").
				Label("Company")).
			Field("phone", model.Char()).
			Field("mobile", model.Char()).
			Field("email", model.Char()).
			Field("street", model.Char()).
			Field("city", model.Char()).
			Field("country_code", model.Char(2)).
			Field("tin", model.Char()).
			Field("website", model.Char()).
			Field("is_customer", model.Boolean().Required().Default("false")).
			Field("is_supplier", model.Boolean().Required().Default("false")).
			Field("is_active", model.Boolean().Required().Default("true")).
			Field("notes", model.Text()).
			Index("idx_contacts_name",
				model.GINIndex("name").WithOps("public.gin_trgm_ops").
					Where("deleted_at IS NULL")).
			Index("idx_contacts_phone",
				model.BTreeIndex("phone").
					Where("phone IS NOT NULL AND deleted_at IS NULL")).
			Index("idx_contacts_is_customer",
				model.BTreeIndex("is_customer", "display_name").
					Where("deleted_at IS NULL")).
			Index("idx_contacts_is_supplier",
				model.BTreeIndex("is_supplier", "display_name").
					Where("deleted_at IS NULL")).
			Index("idx_contacts_company",
				model.BTreeIndex("company_id").
					Where("company_id IS NOT NULL")).
			OnCreate(ContactCreated).
			OnUpdate(ContactUpdated).
			OnDelete(ContactDeleted).
			EnableOps(
				model.List.Requires(ContactRead),
				model.Get.Requires(ContactRead),
				model.Create.Requires(ContactWrite),
				model.Update.Requires(ContactWrite),
				model.Delete.Requires(ContactDelete),
				model.Preview.Requires(ContactWrite),
			),
	},
}
