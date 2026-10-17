package main

import (
	"errors"
	"slices"
	"uuid"

	abi "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/djangbahevans/goerp/modules/contacts/models"
	"github.com/djangbahevans/goerp/modules/contacts/schema"
	"github.com/djangbahevans/goerp/sdk/go/db"
	"github.com/djangbahevans/goerp/sdk/go/engine"
	"github.com/djangbahevans/goerp/sdk/go/orm"
)

func init() {
	engine.HandleAction(engine.DefineAction[models.Address, map[string]any]("create", engine.Requires(schema.ContactWrite)), createAddress)
	engine.HandleAction(engine.DefineAction[models.Address, map[string]any]("update", engine.Requires(schema.ContactWrite)), updateAddress)
	engine.HandleAction(engine.Delete[models.Address](engine.Requires(schema.ContactWrite)), deleteAddress)
}

type addressResponse map[string]any

func (addressResponse) ResourceName() string { return models.Address{}.ResourceName() }

func (response *addressResponse) Scan(record map[string]any) error {
	*response = record
	return nil
}

type addressTarget struct {
	ContactID string `json:"contact_id"`
	IsPrimary bool   `json:"is_primary"`
}

func createAddress(req *engine.Request, body map[string]any) *engine.Response {
	return saveAddress(req, body, false)
}

func updateAddress(req *engine.Request, body map[string]any) *engine.Response {
	return saveAddress(req, body, false)
}

func deleteAddress(req *engine.Request, _ engine.NoBody) *engine.Response {
	return saveAddress(req, nil, true)
}

func saveAddress(req *engine.Request, bodyValues map[string]any, archive bool) *engine.Response {
	// Extension fields are validated by the resolved ORM model, beyond the generated Go model's field set.
	vals := orm.NewValues[models.Address]()
	for field, value := range bodyValues {
		vals.Set(orm.NewField[models.Address, any](field), value)
	}

	id := req.PathParam("id")
	var result models.Address
	var body addressResponse
	err := db.WithTx(func(tx *db.Tx) error {
		var old models.Address
		if id != "" {
			if err := tx.Lock("contacts.address:" + id); err != nil {
				return err
			}

			var err error
			old, err = orm.GetTx[models.Address](tx, id)
			if err != nil {
				return err
			}
		}
		target := addressTarget{
			ContactID: old.ContactID,
			IsPrimary: old.IsPrimary,
		}
		if !archive {
			if err := req.ParseJSON(&target); err != nil {
				return &abi.HostError{Code: abi.ErrCodeValidationFailed, Message: "invalid address fields"}
			}
		}

		contactID, err := uuid.Parse(target.ContactID)
		if err != nil {
			return &abi.HostError{
				Code:    abi.ErrCodeValidationFailed,
				Message: "select a contact",
				Details: map[string]any{"field": "contact_id"},
			}
		}

		target.ContactID = contactID.String()
		vals.Set(models.AddressFields.ContactID, target.ContactID)

		// Contact locks serialize first-address selection and moves between contacts.
		contacts := []string{old.ContactID, target.ContactID}
		slices.Sort(contacts)
		for _, contactID := range slices.Compact(contacts) {
			if contactID == "" {
				continue
			}
			if err := tx.Lock("contacts.addresses:" + contactID); err != nil {
				return err
			}
			if _, err := orm.GetTx[models.Contact](tx, contactID); err != nil {
				return err
			}
		}

		if archive {
			if _, err := orm.UnlinkTx[models.Address](tx, id); err != nil {
				return err
			}
		} else {
			primary, err := addressPrimary(tx, target.ContactID)
			if err != nil {
				return err
			}
			target.IsPrimary = target.IsPrimary || primary == ""
			if target.IsPrimary && primary != "" && primary != id {
				if err := orm.WriteTx(tx, primary, orm.NewValues[models.Address]().Set(models.AddressFields.IsPrimary, false), nil); err != nil {
					return err
				}
			}
			vals.Set(models.AddressFields.IsPrimary, target.IsPrimary)
			if id == "" {
				result, err = orm.CreateTx[models.Address](tx, vals)
			} else {
				var expected *string
				if values := req.HeaderAll("If-Match"); len(values) > 0 {
					expected = new(values[0])
				}
				err = orm.WriteTx(tx, id, vals, expected)
				result.ID = id
			}
			if err != nil {
				return err
			}
		}

		if old.ContactID != "" {
			if err := ensureAddressPrimary(tx, old.ContactID); err != nil {
				return err
			}
		}
		if !archive {
			if err := ensureAddressPrimary(tx, target.ContactID); err != nil {
				return err
			}

			var err error
			body, err = orm.GetTx[addressResponse](tx, result.ID)
			return err
		}
		return nil
	})
	if err != nil {
		return addressError(err)
	}
	if archive {
		return engine.NoContent()
	}

	if id == "" {
		return engine.Created(body)
	}
	return engine.OK(body)
}

func addressGroup(contactID string) orm.Condition[models.Address] {
	return models.AddressFields.ContactID.Eq(contactID)
}

func addressPrimary(tx *db.Tx, contactID string) (string, error) {
	row, err := orm.From[models.Address]().Tx(tx).
		Where(addressGroup(contactID).And(models.AddressFields.IsPrimary.Eq(true))).
		Select(models.AddressFields.ID).One()
	if errors.Is(err, orm.ErrNotFound) {
		return "", nil
	}
	return row.ID, err
}

func ensureAddressPrimary(tx *db.Tx, contactID string) error {
	primary, err := addressPrimary(tx, contactID)
	if err != nil || primary != "" {
		return err
	}
	first, err := orm.From[models.Address]().Tx(tx).Where(addressGroup(contactID)).
		Select(models.AddressFields.ID, models.AddressFields.CreatedAt).
		OrderBy(models.AddressFields.CreatedAt, false).One()
	if errors.Is(err, orm.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	first, err = orm.From[models.Address]().Tx(tx).
		Where(addressGroup(contactID).And(models.AddressFields.CreatedAt.Eq(first.CreatedAt))).
		Select(models.AddressFields.ID).OrderBy(models.AddressFields.ID, false).One()
	if err != nil {
		return err
	}
	return orm.WriteTx(tx, first.ID, orm.NewValues[models.Address]().Set(models.AddressFields.IsPrimary, true), nil)
}

func addressError(err error) *engine.Response {
	if errors.Is(err, orm.ErrNotFound) {
		return engine.NotFound("contacts.address.not_found", nil)
	}
	if host, ok := errors.AsType[*abi.HostError](err); ok {
		switch host.Code {
		case abi.ErrCodeNotFound:
			return engine.NotFound("contacts.address.not_found", nil)
		case abi.ErrCodeValidationFailed, abi.ErrCodeFieldUnknown, abi.ErrCodeFieldNotWritable:
			return engine.Error(400, "contacts.address.validation_failed", host.Message, host.Details)
		case abi.ErrCodeFieldWriteDenied, abi.ErrCodeFieldReadDenied, abi.ErrCodeCapabilityDenied:
			return engine.Forbidden("contacts.address.forbidden", nil)
		case abi.ErrCodeEtagMismatch, abi.ErrCodeUniqueViolation, abi.ErrCodeForeignKeyViolation:
			return engine.Conflict("contacts.address.conflict", host.Details)
		}
	}
	return engine.Error(500, "contacts.address.failed", "Could not save the address", nil)
}
