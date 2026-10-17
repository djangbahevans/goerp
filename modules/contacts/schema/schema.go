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
			Field("tag_ids", model.Many2Many("contacts.tag",
				model.Through("contact_tag_assignments"), model.M2MColumn1("contact_id"), model.M2MColumn2("tag_id"), model.RetainArchived())).
			Field("channel_ids", model.One2Many("contacts.channel", "contact_id")).
			Field("phone", model.Char().Computed("_compute_phone").Store(true).Depends("channel_ids.value", "channel_ids.is_primary", "channel_ids.kind")).
			Field("phone_digits", model.Char().Computed("_compute_phone_digits").Store(true).Depends("channel_ids.value", "channel_ids.is_primary", "channel_ids.kind")).
			Field("email", model.Char().Computed("_compute_email").Store(true).Depends("channel_ids.value", "channel_ids.is_primary", "channel_ids.kind")).
			Field("address_ids", model.One2Many("contacts.address", "contact_id")).
			Field("city", model.Char().Computed("_compute_city").Store(true).Depends("address_ids.city", "address_ids.is_primary")).
			Field("country_code", model.Char(2).Computed("_compute_country_code").Store(true).Depends("address_ids.country_code", "address_ids.is_primary")).
			Field("identifier_ids", model.One2Many("contacts.identifier", "contact_id")).
			Field("tax_id", model.Char().Computed("_compute_tax_id").Store(true).Depends("identifier_ids.value", "identifier_ids.is_tax")).
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
		model.Define("contacts.address", model.Table("contact_addresses"), model.Label("Address"), model.LabelPlural("Addresses")).
			WithStandardFields().
			Field("contact_id", model.Many2One("contacts.contact").Required()).
			Field("type", model.Selection("home", "postal", "billing", "delivery", "other").Required().Default("'other'")).
			Field("label", model.Char()).
			Field("street", model.Char()).
			Field("street2", model.Char()).
			Field("city", model.Char()).
			Field("postal_code", model.Char()).
			Field("country_code", model.Char(2)).
			Field("is_primary", model.Boolean().Required().Default("false")).
			Index("idx_contact_addresses_primary", model.BTreeIndex("contact_id").Unique().Where("is_primary AND deleted_at IS NULL")).
			EnableOps(
				model.List.Requires(ContactRead),
				model.Get.Requires(ContactRead),
				model.Create.Requires(ContactWrite),
				model.Update.Requires(ContactWrite),
				model.Delete.Requires(ContactWrite),
				model.Preview.Requires(ContactWrite),
			),
		model.Define("contacts.identifier_type", model.Table("contact_identifier_types"), model.Label("Identifier Type"), model.LabelPlural("Identifier Types")).
			WithStandardFields().
			Field("code", model.Char().Required()).
			Field("label", model.Char().Required().Primary()).
			Field("applies_to", model.Selection("person", "organization", "any").Required().Default("'any'")).
			Field("country_code", model.Char(2)).
			Field("pattern", model.Char()).
			Field("uppercase", model.Boolean().Required().Default("false")).
			Field("is_unique", model.Boolean().Required().Default("true")).
			Field("is_tax", model.Boolean().Required().Default("false")).
			Field("is_sensitive", model.Boolean().Required().Default("false")).
			Field("owner_module", model.Char().Readonly()).
			Field("is_active", model.Boolean().Required().Default("true")).
			Index("idx_contact_identifier_types_code", model.BTreeIndex("code").Unique()).
			EnableOps(
				model.List.Requires(ContactRead),
				model.Get.Requires(ContactRead),
				model.Create.Requires(ConfigWrite),
				model.Update.Requires(ConfigWrite),
				model.Delete.Requires(ConfigWrite),
				model.Preview.Requires(ConfigWrite),
			),
		model.Define("contacts.identifier", model.Table("contact_identifiers"), model.Label("Identifier"), model.LabelPlural("Identifiers")).
			WithStandardFields().
			Field("contact_id", model.Many2One("contacts.contact").Required()).
			Field("type_id", model.Many2One("contacts.identifier_type").Required()).
			Field("value", model.Char().Required().Primary()).
			Field("expires_on", model.Date()).
			Field("unique_key", model.Char().Computed("_compute_identifier_unique_key").Store(true).Depends("value", "type_id", "type.is_unique")).
			Field("is_tax", model.Boolean().Required().Default("false").Computed("_compute_identifier_is_tax").Store(true).Depends("type_id", "type.is_tax")).
			Field("is_sensitive", model.Boolean().Required().Default("false").Computed("_compute_identifier_is_sensitive").Store(true).Depends("type_id", "type.is_sensitive")).
			Index("idx_contact_identifiers_unique", model.BTreeIndex("type_id", "unique_key").Unique().Where("unique_key IS NOT NULL AND deleted_at IS NULL")).
			Index("idx_contact_identifiers_one_per_type", model.BTreeIndex("contact_id", "type_id").Unique().Where("deleted_at IS NULL")).
			Index("idx_contact_identifiers_one_tax", model.BTreeIndex("contact_id").Unique().Where("is_tax AND deleted_at IS NULL")).
			EnableOps(
				model.List.Requires(IdentifierRead),
				model.Get.Requires(IdentifierRead),
				model.Create.Requires(IdentifierWrite),
				model.Update.Requires(IdentifierWrite),
				model.Delete.Requires(IdentifierWrite),
				model.Preview.Requires(IdentifierWrite),
			),
		model.Define("contacts.tag", model.Table("contact_tags"), model.Label("Tag"), model.LabelPlural("Tags")).
			WithStandardFields().
			Field("name", model.Char().Required().Primary()).
			Field("color", model.Char()).
			Index("idx_contact_tags_name", model.BTreeIndex("lower(name)").Unique().Where("deleted_at IS NULL")).
			EnableOps(
				model.List.Requires(ContactRead),
				model.Get.Requires(ContactRead),
				model.Create.Requires(ConfigWrite),
				model.Update.Requires(ConfigWrite),
				model.Delete.Requires(ConfigWrite),
				model.Preview.Requires(ConfigWrite),
			),
	},
}
