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
			Field("channel_ids", model.One2Many("contacts.channel", "contact_id")).
			Field("phone", model.Char().Computed("_compute_phone").Store(true).Depends("channel_ids.value", "channel_ids.is_primary", "channel_ids.kind")).
			Field("phone_digits", model.Char().Computed("_compute_phone_digits").Store(true).Depends("channel_ids.value", "channel_ids.is_primary", "channel_ids.kind")).
			Field("email", model.Char().Computed("_compute_email").Store(true).Depends("channel_ids.value", "channel_ids.is_primary", "channel_ids.kind")).
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
			Index("idx_contacts_phone", model.BTreeIndex("phone").Where("phone IS NOT NULL AND deleted_at IS NULL")).
			Index("idx_contacts_phone_digits", model.BTreeIndex("phone_digits").Where("phone_digits IS NOT NULL AND deleted_at IS NULL")).
			Index("idx_contacts_email", model.BTreeIndex("lower(email)").Where("email IS NOT NULL AND deleted_at IS NULL")).
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
		model.Define("contacts.channel", model.Table("contact_channels"), model.Label("Channel"), model.LabelPlural("Channels")).
			WithStandardFields().
			Field("contact_id", model.Many2One("contacts.contact").Required()).
			Field("kind", model.Selection("phone", "email").Required()).
			Field("value", model.Char().Required()).
			Field("label", model.Selection("mobile", "home", "work", "other").Required().Default("'other'")).
			Field("is_primary", model.Boolean().Required().Default("false")).
			Field("whatsapp", model.Boolean().Required().Default("false")).
			Field("marketing_consent", model.Selection("unknown", "granted", "withdrawn").Required().Default("'unknown'")).
			Field("consent_updated_at", model.TimestampTZ()).
			Field("consent_source", model.Char()).
			Index("idx_contact_channels_primary", model.BTreeIndex("contact_id", "kind").Unique().Where("is_primary AND deleted_at IS NULL")).
			Index("idx_contact_channels_value", model.BTreeIndex("kind", "lower(value)").Where("deleted_at IS NULL")).
			EnableOps(
				model.List.Requires(ContactRead),
				model.Get.Requires(ContactRead),
				model.Create.Requires(ContactWrite),
				model.Update.Requires(ContactWrite),
				model.Delete.Requires(ContactWrite),
				model.Preview.Requires(ContactWrite),
			),
	},
}
