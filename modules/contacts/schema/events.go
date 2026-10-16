package schema

import "github.com/djangbahevans/goerp/sdk/go/events/def"

type ContactCreatedPayload struct {
	ContactID   string  `msgpack:"contact_id" record:"id"`
	Type        string  `msgpack:"type"`
	DisplayName string  `msgpack:"display_name"`
	Email       *string `msgpack:"email"`
	Phone       *string `msgpack:"phone"`
}

type ContactUpdatedPayload struct {
	ContactID     string   `msgpack:"contact_id" record:"id"`
	ChangedFields []string `msgpack:"changed_fields"`
	DisplayName   string   `msgpack:"display_name"`
	Email         *string  `msgpack:"email"`
	Phone         *string  `msgpack:"phone"`
}

type ContactDeletedPayload struct {
	ContactID   string `msgpack:"contact_id" record:"id"`
	DisplayName string `msgpack:"display_name"`
}

var (
	ContactCreated = def.Define[ContactCreatedPayload]("contacts.contact.created",
		def.Description("Emitted when a contact is created"))
	ContactUpdated = def.Define[ContactUpdatedPayload]("contacts.contact.updated",
		def.Description("Emitted when a contact is updated, with the fields that changed"))
	ContactDeleted = def.Define[ContactDeletedPayload]("contacts.contact.deleted",
		def.Description("Emitted when a contact is archived"))
)
