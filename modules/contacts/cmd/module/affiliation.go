package main

import (
	"errors"
	"slices"
	"time"
	"uuid"

	abi "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/djangbahevans/goerp/modules/contacts/models"
	"github.com/djangbahevans/goerp/modules/contacts/schema"
	"github.com/djangbahevans/goerp/sdk/go/db"
	"github.com/djangbahevans/goerp/sdk/go/engine"
	"github.com/djangbahevans/goerp/sdk/go/orm"
)

func init() {
	engine.HandleAction(engine.DefineAction[models.Affiliation, map[string]any]("create", engine.Requires(schema.ContactWrite)), createAffiliation)
	engine.HandleAction(engine.DefineAction[models.Affiliation, map[string]any]("update", engine.Requires(schema.ContactWrite)), updateAffiliation)
	engine.HandleAction(engine.Delete[models.Affiliation](engine.Requires(schema.ContactWrite)), deleteAffiliation)
	orm.RegisterConstraint[models.Affiliation](orm.OnCreate, checkAffiliation)
	orm.RegisterConstraint[models.Affiliation](orm.OnWrite, checkAffiliation)
}

type affiliationResponse map[string]any

func (affiliationResponse) ResourceName() string { return models.Affiliation{}.ResourceName() }

func (response *affiliationResponse) Scan(record map[string]any) error {
	*response = record
	return nil
}

func createAffiliation(req *engine.Request, body map[string]any) *engine.Response {
	return saveAffiliation(req, body, false)
}

func updateAffiliation(req *engine.Request, body map[string]any) *engine.Response {
	return saveAffiliation(req, body, false)
}

func deleteAffiliation(req *engine.Request, _ engine.NoBody) *engine.Response {
	return saveAffiliation(req, nil, true)
}

func saveAffiliation(req *engine.Request, body map[string]any, archive bool) *engine.Response {
	id, err := canonicalIdentifierRecordID(req)
	if err != nil {
		return affiliationError(err)
	}
	var response affiliationResponse
	err = db.WithTx(func(tx *db.Tx) error {
		var old models.Affiliation
		if id != "" {
			if err := tx.Lock("contacts.affiliation:" + id); err != nil {
				return err
			}

			var err error
			old, err = orm.GetTx[models.Affiliation](tx, id)
			if err != nil {
				return err
			}
		}

		personID, organizationID := old.PersonID, old.OrganizationID
		for field, target := range map[string]*string{"person_id": &personID, "organization_id": &organizationID} {
			if value, supplied := body[field]; supplied {
				text, _ := value.(string)
				parsed, err := uuid.Parse(text)
				if err != nil {
					return validationError(field, "select a valid contact")
				}
				*target = parsed.String()
			}
			if *target == "" {
				return validationError(field, "select a contact")
			}
		}

		// Row locks serialize affiliation selection with contact kind and activity changes.
		parties := []string{old.PersonID, old.OrganizationID, personID, organizationID}
		slices.Sort(parties)
		for _, party := range slices.Compact(parties) {
			if party != "" {
				if _, err := tx.Query[struct{ ID string }]("SELECT id FROM contacts WHERE id = $1 FOR UPDATE", []any{party}); err != nil {
					return err
				}
			}
		}

		if archive {
			if _, err := orm.UnlinkTx[models.Affiliation](tx, id); err != nil {
				return err
			}
			return ensureAffiliationPrimary(tx, old.PersonID)
		}

		for _, party := range []struct {
			field string
			id    string
			oldID string
			kind  models.ContactType
		}{
			{"person_id", personID, old.PersonID, "person"},
			{"organization_id", organizationID, old.OrganizationID, "organization"},
		} {
			if id != "" && party.id == party.oldID {
				continue
			}
			contact, err := orm.GetTx[models.Contact](tx, party.id)
			if err != nil {
				return err
			}
			if contact.Type != party.kind || !contact.IsActive {
				return validationError(party.field, "select an active "+string(party.kind))
			}
		}

		vals := orm.NewValues[models.Affiliation]()
		for field, value := range body {
			vals.Set(orm.NewField[models.Affiliation, any](field), value)
		}
		vals.Set(models.AffiliationFields.PersonID, personID).
			Set(models.AffiliationFields.OrganizationID, organizationID)

		endDate := old.EndDate
		if value, supplied := body["end_date"]; supplied {
			endDate = nil
			if value != nil && value != "" {
				text, ok := value.(string)
				date, err := time.Parse(time.DateOnly, text)
				if !ok || err != nil {
					return validationError("end_date", "enter a valid date")
				}
				endDate = new(date)
			}
		}
		primary := old.IsPrimary
		if value, supplied := body["is_primary"]; supplied {
			var ok bool
			primary, ok = value.(bool)
			if !ok {
				return validationError("is_primary", "enter a boolean")
			}
		}
		if affiliationEnded(endDate) {
			if body["is_primary"] == true {
				return validationError("is_primary", "an ended affiliation cannot be primary")
			}
			primary = false
		} else {
			current, err := affiliationPrimary(tx, personID)
			if err != nil {
				return err
			}
			primary = primary || current == ""
			if primary && current != "" && current != id {
				if err := orm.WriteTx(tx, current, orm.NewValues[models.Affiliation]().Set(models.AffiliationFields.IsPrimary, false), nil); err != nil {
					return err
				}
			}
		}
		vals.Set(models.AffiliationFields.IsPrimary, primary)
		if id == "" {
			created, err := orm.CreateTx[models.Affiliation](tx, vals)
			if err != nil {
				return err
			}
			id = created.ID
		} else if err := orm.WriteTx(tx, id, vals, expectedIdentifierEtag(req)); err != nil {
			return err
		}

		if old.PersonID != "" && old.PersonID != personID {
			if err := ensureAffiliationPrimary(tx, old.PersonID); err != nil {
				return err
			}
		}
		if err := ensureAffiliationPrimary(tx, personID); err != nil {
			return err
		}
		var err error
		response, err = orm.GetTx[affiliationResponse](tx, id)
		return err
	})
	if err != nil {
		return affiliationError(err)
	}
	if archive {
		return engine.NoContent()
	}
	if req.PathParam("id") == "" {
		return engine.Created(response)
	}
	return engine.OK(response)
}

func currentAffiliations(personID string) orm.Condition[models.Affiliation] {
	return models.AffiliationFields.PersonID.Eq(personID).And(
		models.AffiliationFields.EndDate.IsNull().Or(orm.Raw[models.Affiliation](orm.Domain("record.end_date >= ?", time.Now().UTC().Format(time.DateOnly)))),
	)
}

func affiliationPrimary(tx *db.Tx, personID string) (string, error) {
	row, err := orm.From[models.Affiliation]().Tx(tx).
		Where(models.AffiliationFields.PersonID.Eq(personID).And(models.AffiliationFields.IsPrimary.Eq(true))).
		Select(models.AffiliationFields.ID, models.AffiliationFields.EndDate).One()
	if errors.Is(err, orm.ErrNotFound) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	if affiliationEnded(row.EndDate) {
		err := orm.WriteTx(tx, row.ID, orm.NewValues[models.Affiliation]().Set(models.AffiliationFields.IsPrimary, false), nil)
		return "", err
	}
	return row.ID, nil
}

func ensureAffiliationPrimary(tx *db.Tx, personID string) error {
	primary, err := affiliationPrimary(tx, personID)
	if err != nil || primary != "" {
		return err
	}
	first, err := orm.From[models.Affiliation]().Tx(tx).Where(currentAffiliations(personID)).
		Select(models.AffiliationFields.ID, models.AffiliationFields.CreatedAt).
		OrderBy(models.AffiliationFields.CreatedAt, false).One()
	if errors.Is(err, orm.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	first, err = orm.From[models.Affiliation]().Tx(tx).
		Where(currentAffiliations(personID).And(models.AffiliationFields.CreatedAt.Eq(first.CreatedAt))).
		Select(models.AffiliationFields.ID).OrderBy(models.AffiliationFields.ID, false).One()
	if err != nil {
		return err
	}
	return orm.WriteTx(tx, first.ID, orm.NewValues[models.Affiliation]().Set(models.AffiliationFields.IsPrimary, true), nil)
}

func affiliationEnded(end *time.Time) bool {
	return end != nil && end.Format(time.DateOnly) < time.Now().UTC().Format(time.DateOnly)
}

func checkAffiliation(_ orm.ConstraintContext, record map[string]any) *orm.ConstraintResult {
	if stringValue(record, "person_id") == stringValue(record, "organization_id") {
		return orm.Reject("organization_id", "a person cannot be their own organization")
	}
	for field, kind := range map[string]string{"person_id": "person", "organization_id": "organization"} {
		// Historical affiliations retain archived parties, whose kind still constrains the link.
		party, err := db.QueryOne[struct{ Type string }]("SELECT type FROM contacts WHERE id = $1", []any{stringValue(record, field)})
		if err != nil || party.Type != kind {
			return orm.Reject(field, "select a "+kind)
		}
	}
	start, _ := record["start_date"].(time.Time)
	end, _ := record["end_date"].(time.Time)
	if !start.IsZero() && !end.IsZero() && end.Before(start) {
		return orm.Reject("end_date", "end date must be on or after start date")
	}
	if !end.IsZero() && affiliationEnded(&end) && record["is_primary"] == true {
		return orm.Reject("is_primary", "an ended affiliation cannot be primary")
	}
	return orm.Allow()
}

func affiliationError(err error) *engine.Response {
	if errors.Is(err, orm.ErrNotFound) {
		return engine.NotFound("contacts.affiliation.not_found", nil)
	}
	if host, ok := errors.AsType[*abi.HostError](err); ok {
		switch host.Code {
		case abi.ErrCodeNotFound:
			return engine.NotFound("contacts.affiliation.not_found", nil)
		case abi.ErrCodeValidationFailed, abi.ErrCodeFieldUnknown, abi.ErrCodeFieldNotWritable:
			return engine.Error(400, "contacts.affiliation.validation_failed", host.Message, host.Details)
		case abi.ErrCodeFieldWriteDenied, abi.ErrCodeFieldReadDenied, abi.ErrCodeCapabilityDenied, abi.ErrCodeAuthzForbidden:
			return engine.Forbidden("contacts.affiliation.forbidden", nil)
		case abi.ErrCodeEtagMismatch, abi.ErrCodeUniqueViolation, abi.ErrCodeForeignKeyViolation:
			return engine.Conflict("contacts.affiliation.conflict", host.Details)
		}
	}
	return engine.Error(500, "contacts.affiliation.failed", "Could not save the affiliation", nil)
}
