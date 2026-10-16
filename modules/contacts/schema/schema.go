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
			Field("type", model.Selection("person", "organization").Required()).
			Field("title", model.Char()).
			Field("given_name", model.Char()).
			Field("other_names", model.Char()).
			Field("family_name", model.Char()).
			Field("preferred_name", model.Char()).
			Field("date_of_birth", model.Date().
				Access(model.AccessRead(PersonalRead), model.AccessWrite(PersonalWrite)).
				OnDeniedRead(model.Omit)).
			Field("gender", model.Char().
				Access(model.AccessRead(PersonalRead), model.AccessWrite(PersonalWrite)).
				OnDeniedRead(model.Omit)).
			Field("legal_name", model.Char()).
			Field("trading_name", model.Char()).
			Field("full_name", model.Char().Required().Default("''").Computed("_compute_full_name").Store(true).
				Depends("type", "given_name", "other_names", "family_name", "legal_name")).
			Field("display_name", model.Char().Required().Default("''").Computed("_compute_display_name").Store(true).
				Depends("type", "given_name", "family_name", "preferred_name", "legal_name", "trading_name")).
			Field("sort_name", model.Char().Required().Default("''").Computed("_compute_sort_name").Store(true).
				Depends("type", "given_name", "other_names", "family_name", "preferred_name", "legal_name", "trading_name")).
			Field("phone", model.Char().Computed("_compute_channel_value").Store(true).Depends("type")).
			Field("email", model.Char().Computed("_compute_channel_value").Store(true).Depends("type")).
			Field("avatar_file_id", model.File()).
			Field("language", model.Char()).
			Field("preferred_channel", model.Selection("phone", "sms", "whatsapp", "email")).
			Field("website", model.Char()).
			Field("is_active", model.Boolean().Required().Default("true")).
			Field("notes", model.Text()).
			Index("idx_contacts_full_name",
				model.GINIndex("full_name").WithOps("public.gin_trgm_ops").
					Where("deleted_at IS NULL")).
			Index("idx_contacts_sort_name",
				model.BTreeIndex("sort_name", "id").
					Where("deleted_at IS NULL")).
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
