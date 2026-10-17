package main

import (
	"time"

	"github.com/djangbahevans/goerp/modules/contacts/models"
	"github.com/djangbahevans/goerp/modules/contacts/schema"
	"github.com/djangbahevans/goerp/sdk/go/authz"
	"github.com/djangbahevans/goerp/sdk/go/db"
	"github.com/djangbahevans/goerp/sdk/go/engine"
	"github.com/djangbahevans/goerp/sdk/go/orm"
)

func init() {
	engine.HandleAction(engine.Update[models.IdentifierType](engine.Requires(schema.ConfigWrite), engine.Timeout(5*time.Minute)), updateIdentifierType)
	engine.HandleAction(engine.Delete[models.IdentifierType](engine.Requires(schema.ConfigWrite)), deleteIdentifierType)
}

type identifierTypeResponse map[string]any

func (identifierTypeResponse) ResourceName() string { return models.IdentifierType{}.ResourceName() }

func (response *identifierTypeResponse) Scan(record map[string]any) error {
	*response = record
	return nil
}

func identifierTypeRules(typ models.IdentifierType) map[string]any {
	var pattern, country any
	if typ.Pattern != nil {
		pattern = *typ.Pattern
	}
	if typ.CountryCode != nil {
		country = *typ.CountryCode
	}
	return map[string]any{
		"code":         typ.Code,
		"applies_to":   string(typ.AppliesTo),
		"country_code": country,
		"pattern":      pattern,
		"uppercase":    typ.Uppercase,
		"is_unique":    typ.IsUnique,
		"is_tax":       typ.IsTax,
		"is_sensitive": typ.IsSensitive,
	}
}

func updateIdentifierType(req *engine.Request, vals *orm.Values[models.IdentifierType]) *engine.Response {
	id, err := canonicalIdentifierRecordID(req)
	if err != nil {
		return identifierError(err)
	}
	var body identifierTypeResponse
	err = db.WithTx(func(tx *db.Tx) error {
		if err := tx.Lock("contacts.identifier_type:" + id); err != nil {
			return err
		}
		old, err := orm.GetTx[models.IdentifierType](tx, id)
		if err != nil {
			return err
		}
		var changes map[string]any
		if err := req.ParseJSON(&changes); err != nil {
			return err
		}

		rulesChanged := false
		for field, oldValue := range identifierTypeRules(old) {
			if value, present := changes[field]; present && value != oldValue {
				if old.OwnerModule != nil && *old.OwnerModule != "" {
					return validationError(field, "module-owned identifier type rules cannot be changed")
				}
				rulesChanged = true
			}
		}

		var identifiers []models.Identifier
		if rulesChanged {
			// Full visibility is required so a rule change cannot skip sensitive rows.
			if err := authz.Require(schema.IdentifierRead, ""); err != nil {
				return err
			}
			if old.IsSensitive || changes["is_sensitive"] == true {
				if err := authz.Require(schema.PersonalRead, ""); err != nil {
					return err
				}
				if err := authz.Require(schema.PersonalWrite, ""); err != nil {
					return err
				}
			}

			var cursor string
			for {
				rows, next, err := orm.From[models.Identifier]().Tx(tx).
					Where(models.IdentifierFields.TypeID.Eq(id)).Cursor(cursor).All()
				if err != nil {
					return err
				}
				identifiers = append(identifiers, rows...)
				if next == "" {
					break
				}
				cursor = next
			}
		}

		if err := orm.WriteTx(tx, id, vals, expectedIdentifierEtag(req)); err != nil {
			return err
		}
		typ, err := orm.GetTx[models.IdentifierType](tx, id)
		if err != nil {
			return err
		}
		for _, identifier := range identifiers {
			value := normalizeIdentifier(identifier.Value, typ)
			if message := identifierValueError(value, typ); message != "" {
				return validationError("pattern", "existing identifiers do not satisfy these rules")
			}
			if err := orm.WriteTx(tx, identifier.ID, orm.NewValues[models.Identifier]().Set(models.IdentifierFields.Value.Field, value), nil); err != nil {
				return err
			}
		}

		body, err = orm.GetTx[identifierTypeResponse](tx, id)
		return err
	})
	if err != nil {
		return identifierError(err)
	}
	return engine.OK(body)
}

func deleteIdentifierType(req *engine.Request, _ engine.NoBody) *engine.Response {
	id, err := canonicalIdentifierRecordID(req)
	if err != nil {
		return identifierError(err)
	}
	err = db.WithTx(func(tx *db.Tx) error {
		if err := tx.Lock("contacts.identifier_type:" + id); err != nil {
			return err
		}
		_, err := orm.UnlinkTx[models.IdentifierType](tx, id)
		return err
	})
	if err != nil {
		return identifierError(err)
	}
	return engine.NoContent()
}
