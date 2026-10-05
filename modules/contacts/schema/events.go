package schema

import "github.com/djangbahevans/goerp/sdk/go/events/def"

// ContactCreatedPayload is contact.created's payload.
type ContactCreatedPayload struct {
	ContactID string  `msgpack:"contact_id" record:"id"`
	Type      string  `msgpack:"type"`
	Name      string  `msgpack:"name"`
	Email     *string `msgpack:"email"`
	Phone     *string `msgpack:"phone"`
	CompanyID *string `msgpack:"company_id"`
}

// ContactUpdatedPayload is contact.updated's payload. ChangedFields lists
// the fields the write changed.
type ContactUpdatedPayload struct {
	ContactID     string   `msgpack:"contact_id" record:"id"`
	ChangedFields []string `msgpack:"changed_fields"`
	Name          string   `msgpack:"name"`
	Email         *string  `msgpack:"email"`
}

// ContactDeletedPayload is contact.deleted's payload, read from the contact
// as it was before it was archived.
type ContactDeletedPayload struct {
	ContactID string `msgpack:"contact_id" record:"id"`
	Name      string `msgpack:"name"`
}

var (
	ContactCreated = def.Define[ContactCreatedPayload]("contacts.contact.created",
		def.Description("Emitted when a contact is created"))
	ContactUpdated = def.Define[ContactUpdatedPayload]("contacts.contact.updated",
		def.Description("Emitted when a contact is updated, with the fields that changed"))
	ContactDeleted = def.Define[ContactDeletedPayload]("contacts.contact.deleted",
		def.Description("Emitted when a contact is archived"))
)
