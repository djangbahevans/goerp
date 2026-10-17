package main

import (
	"errors"
	"maps"
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
	engine.HandleAction(engine.DefineAction[models.Relationship, map[string]any]("create", engine.Requires(schema.ContactWrite)), createRelationship)
	engine.HandleAction(engine.DefineAction[models.Relationship, map[string]any]("update", engine.Requires(schema.ContactWrite)), updateRelationship)
	engine.HandleAction(engine.Delete[models.Relationship](engine.Requires(schema.ContactWrite)), deleteRelationship)
	orm.RegisterComputed("_compute_relationship_label", computeRelationshipLabel)
	orm.RegisterConstraint[models.Relationship](orm.OnCreate, checkRelationship)
	orm.RegisterConstraint[models.Relationship](orm.OnWrite, checkRelationship)
}

type relationshipResponse map[string]any

func (relationshipResponse) ResourceName() string { return models.Relationship{}.ResourceName() }

func (response *relationshipResponse) Scan(record map[string]any) error {
	*response = record
	return nil
}

func createRelationship(req *engine.Request, body map[string]any) *engine.Response {
	return saveRelationship(req, body, false)
}

func updateRelationship(req *engine.Request, body map[string]any) *engine.Response {
	return saveRelationship(req, body, false)
}

func deleteRelationship(req *engine.Request, _ engine.NoBody) *engine.Response {
	return saveRelationship(req, nil, true)
}

func saveRelationship(req *engine.Request, body map[string]any, archive bool) *engine.Response {
	for _, field := range []string{"pair_id", "direction", "label"} {
		if _, supplied := body[field]; supplied {
			return relationshipError(validationError(field, "this field is assigned by the relationship type and pair"))
		}
	}

	id, err := canonicalIdentifierRecordID(req)
	if err != nil {
		return relationshipError(err)
	}

	var response relationshipResponse
	err = db.WithTx(func(tx *db.Tx) error {
		var old, paired models.Relationship
		pairID, direction := uuid.New().String(), "forward"
		if id != "" {
			old, err = orm.GetTx[models.Relationship](tx, id)
			if err != nil {
				return err
			}
			pairID = old.PairID
			if err := tx.Lock("contacts.relationship:" + pairID); err != nil {
				return err
			}

			old, err = orm.GetTx[models.Relationship](tx, id)
			if err != nil {
				return err
			}
			direction = string(old.Direction)
			paired, err = orm.From[models.Relationship]().Tx(tx).
				Where(models.RelationshipFields.PairID.Eq(pairID).And(models.RelationshipFields.ID.Eq(id).Not())).One()
			if err != nil {
				return err
			}
		}

		if archive {
			if _, err := orm.UnlinkTx[models.Relationship](tx, id); err != nil {
				return err
			}
			_, err := orm.UnlinkTx[models.Relationship](tx, paired.ID)
			return err
		}

		contactID, relatedID, typeID := old.ContactID, old.RelatedContactID, old.TypeID
		for field, target := range map[string]*string{"contact_id": &contactID, "related_contact_id": &relatedID, "type_id": &typeID} {
			if value, supplied := body[field]; supplied {
				text, _ := value.(string)
				parsed, err := uuid.Parse(text)
				if err != nil {
					return validationError(field, "select a valid record")
				}
				*target = parsed.String()
			}
			if *target == "" {
				return validationError(field, "select a record")
			}
		}

		if err := tx.Lock("contacts.relationship_type:" + typeID); err != nil {
			return err
		}
		typ, err := orm.GetTx[models.RelationshipType](tx, typeID)
		if err != nil {
			return err
		}
		if typeID != old.TypeID && !typ.IsActive {
			return validationError("type_id", "select an active relationship type")
		}

		// Contact row locks serialize selection with contact kind changes and archiving.
		parties := []string{old.ContactID, old.RelatedContactID, contactID, relatedID}
		slices.Sort(parties)
		for _, party := range slices.Compact(parties) {
			if party != "" {
				if _, err := tx.Query[struct{ ID string }]("SELECT id FROM contacts WHERE id = $1 FOR UPDATE", []any{party}); err != nil {
					return err
				}
			}
		}

		for field, party := range map[string]struct{ id, oldID string }{
			"contact_id":         {contactID, old.ContactID},
			"related_contact_id": {relatedID, old.RelatedContactID},
		} {
			if party.id != party.oldID {
				contact, err := orm.GetTx[models.Contact](tx, party.id)
				if err != nil {
					return err
				}
				if !contact.IsActive {
					return validationError(field, "select an active contact")
				}
			}
		}

		values := maps.Clone(body)
		values["contact_id"], values["related_contact_id"], values["type_id"] = contactID, relatedID, typeID
		if id == "" {
			values["pair_id"], values["direction"] = pairID, direction
			created, err := orm.CreateTx[models.Relationship](tx, relationshipValues(values))
			if err != nil {
				return err
			}
			id = created.ID
		} else if err := orm.WriteTx(tx, id, relationshipValues(values), expectedIdentifierEtag(req)); err != nil {
			return err
		}

		values["contact_id"], values["related_contact_id"] = relatedID, contactID
		if paired.ID == "" {
			values["direction"] = "inverse"
			if _, err := orm.CreateTx[models.Relationship](tx, relationshipValues(values)); err != nil {
				return err
			}
		} else if err := orm.WriteTx(tx, paired.ID, relationshipValues(values), nil); err != nil {
			return err
		}

		response, err = orm.GetTx[relationshipResponse](tx, id)
		return err
	})
	if err != nil {
		return relationshipError(err)
	}
	if archive {
		return engine.NoContent()
	}
	if req.PathParam("id") == "" {
		return engine.Created(response)
	}
	return engine.OK(response)
}

func relationshipValues(body map[string]any) *orm.Values[models.Relationship] {
	values := orm.NewValues[models.Relationship]()
	for field, value := range body {
		values.Set(orm.NewField[models.Relationship, any](field), value)
	}
	return values
}

func computeRelationshipLabel(_ orm.ComputeContext, record map[string]any) (any, error) {
	typ, err := orm.Get[models.RelationshipType](stringValue(record, "type_id"))
	if err != nil {
		return nil, err
	}
	if stringValue(record, "direction") == "inverse" && !typ.Symmetric && typ.InverseName != nil {
		return *typ.InverseName, nil
	}
	return typ.Name, nil
}

func checkRelationship(_ orm.ConstraintContext, record map[string]any) *orm.ConstraintResult {
	if stringValue(record, "contact_id") == stringValue(record, "related_contact_id") {
		return orm.Reject("related_contact_id", "a contact cannot be related to itself")
	}

	typ, err := orm.Get[models.RelationshipType](stringValue(record, "type_id"))
	if err != nil {
		return orm.Reject("type_id", "select an available relationship type")
	}
	from, to := string(typ.FromType), string(typ.ToType)
	if stringValue(record, "direction") == "inverse" {
		from, to = to, from
	}
	for field, kind := range map[string]string{"contact_id": from, "related_contact_id": to} {
		// Archived contacts still constrain the kind of historical links.
		party, err := db.QueryOne[struct{ Type string }]("SELECT type FROM contacts WHERE id = $1", []any{stringValue(record, field)})
		if err != nil || party.Type != kind {
			return orm.Reject(field, "select a "+kind)
		}
	}
	return orm.Allow()
}

func relationshipError(err error) *engine.Response {
	if errors.Is(err, orm.ErrNotFound) {
		return engine.NotFound("contacts.relationship.not_found", nil)
	}
	if host, ok := errors.AsType[*abi.HostError](err); ok {
		switch host.Code {
		case abi.ErrCodeNotFound:
			return engine.NotFound("contacts.relationship.not_found", nil)
		case abi.ErrCodeValidationFailed, abi.ErrCodeFieldUnknown, abi.ErrCodeFieldNotWritable:
			return engine.Error(400, "contacts.relationship.validation_failed", host.Message, host.Details)
		case abi.ErrCodeFieldWriteDenied, abi.ErrCodeFieldReadDenied, abi.ErrCodeCapabilityDenied, abi.ErrCodeAuthzForbidden:
			return engine.Forbidden("contacts.relationship.forbidden", nil)
		case abi.ErrCodeEtagMismatch, abi.ErrCodeUniqueViolation, abi.ErrCodeForeignKeyViolation:
			return engine.Conflict("contacts.relationship.conflict", host.Details)
		}
	}
	return engine.Error(500, "contacts.relationship.failed", "Could not save the relationship", nil)
}
