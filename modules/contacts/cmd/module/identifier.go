package main

import (
	"errors"
	"slices"
	"uuid"

	abi "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/djangbahevans/goerp/modules/contacts/models"
	"github.com/djangbahevans/goerp/modules/contacts/schema"
	"github.com/djangbahevans/goerp/sdk/go/authz"
	"github.com/djangbahevans/goerp/sdk/go/db"
	"github.com/djangbahevans/goerp/sdk/go/engine"
	"github.com/djangbahevans/goerp/sdk/go/orm"
)

func init() {
	engine.HandleAction(engine.Create[models.Identifier](engine.Requires(schema.IdentifierRead, schema.IdentifierWrite)), createIdentifier)
	engine.HandleAction(engine.Update[models.Identifier](engine.Requires(schema.IdentifierRead, schema.IdentifierWrite)), updateIdentifier)
	engine.HandleAction(engine.Delete[models.Identifier](engine.Requires(schema.IdentifierRead, schema.IdentifierWrite)), deleteIdentifier)
}

type identifierResponse map[string]any

func (identifierResponse) ResourceName() string { return models.Identifier{}.ResourceName() }

func (response *identifierResponse) Scan(record map[string]any) error {
	*response = record
	return nil
}

type identifierTarget struct {
	ContactID string `json:"contact_id"`
	TypeID    string `json:"type_id"`
	Value     string `json:"value"`
}

func createIdentifier(req *engine.Request, vals *orm.Values[models.Identifier]) *engine.Response {
	return saveIdentifier(req, vals, false)
}

func updateIdentifier(req *engine.Request, vals *orm.Values[models.Identifier]) *engine.Response {
	return saveIdentifier(req, vals, false)
}

func deleteIdentifier(req *engine.Request, _ engine.NoBody) *engine.Response {
	return saveIdentifier(req, nil, true)
}

func saveIdentifier(req *engine.Request, vals *orm.Values[models.Identifier], archive bool) *engine.Response {
	id, err := canonicalIdentifierRecordID(req)
	if err != nil {
		return identifierError(err)
	}
	var body identifierResponse

	err = db.WithTx(func(tx *db.Tx) error {
		var old models.Identifier
		if id != "" {
			if err := tx.Lock("contacts.identifier:" + id); err != nil {
				return err
			}

			var err error
			old, err = orm.GetTx[models.Identifier](tx, id)
			if err != nil {
				return err
			}
		}

		target := identifierTarget{ContactID: old.ContactID, TypeID: old.TypeID, Value: old.Value}
		if !archive {
			if err := req.ParseJSON(&target); err != nil {
				return validationError("value", "invalid identifier fields")
			}
		}

		typeID, err := uuid.Parse(target.TypeID)
		if err != nil {
			return validationError("type_id", "select an identifier type")
		}
		target.TypeID = typeID.String()
		types := []string{old.TypeID, target.TypeID}
		slices.Sort(types)
		for _, typeID := range slices.Compact(types) {
			if typeID == "" {
				continue
			}
			if err := tx.Lock("contacts.identifier_type:" + typeID); err != nil {
				return err
			}
		}

		if archive {
			if old.IsSensitive {
				if err := authz.Require(schema.PersonalWrite, ""); err != nil {
					return err
				}
			}
			_, err := orm.UnlinkTx[models.Identifier](tx, id)
			return err
		}

		typ, err := orm.GetTx[models.IdentifierType](tx, target.TypeID)
		if err != nil {
			return err
		}
		if old.IsSensitive || typ.IsSensitive {
			if err := authz.Require(schema.PersonalRead, ""); err != nil {
				return err
			}
			if err := authz.Require(schema.PersonalWrite, ""); err != nil {
				return err
			}
		}
		if !typ.IsActive && (id == "" || old.TypeID != target.TypeID) {
			return validationError("type_id", "select an active identifier type")
		}

		contactID, err := uuid.Parse(target.ContactID)
		if err != nil {
			return validationError("contact_id", "select a contact")
		}
		if _, err := orm.GetTx[models.Contact](tx, contactID.String()); err != nil {
			return err
		}

		value := normalizeIdentifier(target.Value, typ)
		if message := identifierValueError(value, typ); message != "" {
			return validationError("value", message)
		}
		vals.Set(models.IdentifierFields.ContactID, contactID.String()).
			Set(models.IdentifierFields.TypeID, target.TypeID).
			Set(models.IdentifierFields.Value.Field, value)
		if id == "" {
			created, err := orm.CreateTx[models.Identifier](tx, vals)
			if err != nil {
				return err
			}
			body, err = orm.GetTx[identifierResponse](tx, created.ID)
			return err
		}

		if err := orm.WriteTx(tx, id, vals, expectedIdentifierEtag(req)); err != nil {
			return err
		}
		body, err = orm.GetTx[identifierResponse](tx, id)
		return err
	})
	if err != nil {
		return identifierError(err)
	}
	if archive {
		return engine.NoContent()
	}
	if id == "" {
		return engine.Created(body)
	}
	return engine.OK(body)
}

func canonicalIdentifierRecordID(req *engine.Request) (string, error) {
	id := req.PathParam("id")
	if id == "" {
		return "", nil
	}
	parsed, err := uuid.Parse(id)
	if err != nil {
		return "", validationError("id", "invalid record ID")
	}
	return parsed.String(), nil
}

func validationError(field, message string) error {
	return &abi.HostError{Code: abi.ErrCodeValidationFailed, Message: message, Details: map[string]any{"field": field}}
}

func expectedIdentifierEtag(req *engine.Request) *string {
	if values := req.HeaderAll("If-Match"); len(values) > 0 {
		return new(values[0])
	}
	return nil
}

func identifierError(err error) *engine.Response {
	if errors.Is(err, orm.ErrNotFound) {
		return engine.NotFound("contacts.identifier.not_found", nil)
	}
	if host, ok := errors.AsType[*abi.HostError](err); ok {
		switch host.Code {
		case abi.ErrCodeNotFound:
			return engine.NotFound("contacts.identifier.not_found", nil)
		case abi.ErrCodeValidationFailed, abi.ErrCodeFieldUnknown, abi.ErrCodeFieldNotWritable:
			return engine.Error(400, "contacts.identifier.validation_failed", host.Message, host.Details)
		case abi.ErrCodeFieldWriteDenied, abi.ErrCodeFieldReadDenied, abi.ErrCodeCapabilityDenied, abi.ErrCodeAuthzForbidden:
			return engine.Forbidden("contacts.identifier.forbidden", nil)
		case abi.ErrCodeEtagMismatch, abi.ErrCodeUniqueViolation, abi.ErrCodeForeignKeyViolation:
			return engine.Conflict("contacts.identifier.conflict", host.Details)
		}
	}
	return engine.Error(500, "contacts.identifier.failed", "Could not save the identifier", nil)
}
