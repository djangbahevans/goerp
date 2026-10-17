package main

import (
	"github.com/djangbahevans/goerp/modules/contacts/models"
	"github.com/djangbahevans/goerp/modules/contacts/schema"
	"github.com/djangbahevans/goerp/sdk/go/db"
	"github.com/djangbahevans/goerp/sdk/go/engine"
	"github.com/djangbahevans/goerp/sdk/go/orm"
)

func init() {
	engine.HandleAction(engine.DefineAction[models.RelationshipType, map[string]any]("update", engine.Requires(schema.ConfigWrite)), updateRelationshipType)
	engine.HandleAction(engine.Delete[models.RelationshipType](engine.Requires(schema.ConfigWrite)), deleteRelationshipType)
	orm.RegisterConstraint[models.RelationshipType](orm.OnCreate, checkRelationshipType)
	orm.RegisterConstraint[models.RelationshipType](orm.OnWrite, checkRelationshipType)
	orm.RegisterConstraint[models.RelationshipType](orm.OnDelete, checkRelationshipTypeDelete)
}

type relationshipTypeResponse map[string]any

func (relationshipTypeResponse) ResourceName() string {
	return models.RelationshipType{}.ResourceName()
}

func (response *relationshipTypeResponse) Scan(record map[string]any) error {
	*response = record
	return nil
}

func updateRelationshipType(req *engine.Request, body map[string]any) *engine.Response {
	id, err := canonicalIdentifierRecordID(req)
	if err != nil {
		return relationshipError(err)
	}

	var response relationshipTypeResponse
	err = db.WithTx(func(tx *db.Tx) error {
		if err := tx.Lock("contacts.relationship_type:" + id); err != nil {
			return err
		}
		old, err := orm.GetTx[models.RelationshipType](tx, id)
		if err != nil {
			return err
		}
		if old.OwnerModule != nil && *old.OwnerModule != "" {
			for field, value := range map[string]any{
				"code":      old.Code,
				"symmetric": old.Symmetric,
				"from_type": string(old.FromType),
				"to_type":   string(old.ToType),
			} {
				if supplied, present := body[field]; present && supplied != value {
					return validationError(field, "module-owned relationship type rules cannot be changed")
				}
			}
		}

		values := orm.NewValues[models.RelationshipType]()
		for field, value := range body {
			values.Set(orm.NewField[models.RelationshipType, any](field), value)
		}
		if err := orm.WriteTx(tx, id, values, expectedIdentifierEtag(req)); err != nil {
			return err
		}
		response, err = orm.GetTx[relationshipTypeResponse](tx, id)
		return err
	})
	if err != nil {
		return relationshipError(err)
	}
	return engine.OK(response)
}

func deleteRelationshipType(req *engine.Request, _ engine.NoBody) *engine.Response {
	id, err := canonicalIdentifierRecordID(req)
	if err != nil {
		return relationshipError(err)
	}
	err = db.WithTx(func(tx *db.Tx) error {
		if err := tx.Lock("contacts.relationship_type:" + id); err != nil {
			return err
		}
		_, err := orm.UnlinkTx[models.RelationshipType](tx, id)
		return err
	})
	if err != nil {
		return relationshipError(err)
	}
	return engine.NoContent()
}

func checkRelationshipType(_ orm.ConstraintContext, record map[string]any) *orm.ConstraintResult {
	for _, field := range []string{"code", "name"} {
		if stringValue(record, field) == "" {
			return orm.Reject(field, "enter a relationship type "+field)
		}
	}
	symmetric := record["symmetric"] == true
	if symmetric != (record["inverse_name"] == nil) {
		return orm.Reject("inverse_name", "symmetric types need no inverse name; directed types require one")
	}
	if !symmetric && stringValue(record, "inverse_name") == "" {
		return orm.Reject("inverse_name", "enter an inverse name")
	}
	if symmetric && stringValue(record, "from_type") != stringValue(record, "to_type") {
		return orm.Reject("to_type", "symmetric types must connect the same party kind")
	}

	invalid, err := db.QueryOne[struct{ Count int }](
		`SELECT count(*) AS count FROM contact_relationships r
		 JOIN contacts c ON c.id = r.contact_id JOIN contacts other ON other.id = r.related_contact_id
		 WHERE r.type_id = $1 AND r.deleted_at IS NULL AND
		 ((r.direction = 'forward' AND (c.type <> $2 OR other.type <> $3)) OR
		  (r.direction = 'inverse' AND (c.type <> $3 OR other.type <> $2)))`,
		[]any{stringValue(record, "id"), stringValue(record, "from_type"), stringValue(record, "to_type")})
	if err != nil || invalid.Count != 0 {
		return orm.Reject("from_type", "party kinds conflict with existing relationships")
	}
	return orm.Allow()
}

func checkRelationshipTypeDelete(_ orm.ConstraintContext, record map[string]any) *orm.ConstraintResult {
	if stringValue(record, "owner_module") != "" {
		return orm.Reject("owner_module", "module-owned relationship types cannot be archived")
	}
	return orm.Allow()
}
