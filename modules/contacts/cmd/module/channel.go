package main

import (
	"errors"
	"slices"

	abi "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/djangbahevans/goerp/modules/contacts/models"
	"github.com/djangbahevans/goerp/modules/contacts/schema"
	"github.com/djangbahevans/goerp/sdk/go/db"
	"github.com/djangbahevans/goerp/sdk/go/engine"
	"github.com/djangbahevans/goerp/sdk/go/orm"
)

func init() {
	engine.HandleAction(engine.Create[models.Channel](engine.Requires(schema.ContactWrite)), createChannel)
	engine.HandleAction(engine.Update[models.Channel](engine.Requires(schema.ContactWrite)), updateChannel)
	engine.HandleAction(engine.Delete[models.Channel](engine.Requires(schema.ContactWrite)), deleteChannel)
}

type channelTarget struct {
	ContactID string             `json:"contact_id"`
	Kind      models.ChannelKind `json:"kind"`
	IsPrimary bool               `json:"is_primary"`
}

func createChannel(req *engine.Request, vals *orm.Values[models.Channel]) *engine.Response {
	return saveChannel(req, vals, false)
}

func updateChannel(req *engine.Request, vals *orm.Values[models.Channel]) *engine.Response {
	return saveChannel(req, vals, false)
}

func deleteChannel(req *engine.Request, _ engine.NoBody) *engine.Response {
	return saveChannel(req, nil, true)
}

func saveChannel(req *engine.Request, vals *orm.Values[models.Channel], archive bool) *engine.Response {
	id := req.PathParam("id")
	var result models.Channel
	err := db.WithTx(func(tx *db.Tx) error {
		var old models.Channel
		if id != "" {
			if err := tx.Lock("contacts.channel:" + id); err != nil {
				return err
			}
			var err error
			old, err = orm.GetTx[models.Channel](tx, id)
			if err != nil {
				return err
			}
		}
		target := channelTarget{
			ContactID: old.ContactID,
			Kind:      old.Kind,
			IsPrimary: old.IsPrimary,
		}
		if !archive {
			if err := req.ParseJSON(&target); err != nil {
				return err
			}
		}

		// Contact locks serialize first-channel selection and moves between contacts.
		contacts := []string{old.ContactID, target.ContactID}
		slices.Sort(contacts)
		for _, contactID := range slices.Compact(contacts) {
			if contactID == "" {
				continue
			}
			if err := tx.Lock("contacts.channels:" + contactID); err != nil {
				return err
			}
			if _, err := orm.GetTx[models.Contact](tx, contactID); err != nil {
				return err
			}
		}

		if archive {
			if _, err := orm.UnlinkTx[models.Channel](tx, id); err != nil {
				return err
			}
		} else {
			primary, err := channelPrimary(tx, target.ContactID, target.Kind)
			if err != nil {
				return err
			}
			target.IsPrimary = target.IsPrimary || primary == ""
			if target.IsPrimary && primary != "" && primary != id {
				if err := orm.WriteTx(tx, primary, orm.NewValues[models.Channel]().Set(models.ChannelFields.IsPrimary, false), nil); err != nil {
					return err
				}
			}
			vals.Set(models.ChannelFields.IsPrimary, target.IsPrimary)
			if id == "" {
				result, err = orm.CreateTx[models.Channel](tx, vals)
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
			if err := ensureChannelPrimary(tx, old.ContactID, old.Kind); err != nil {
				return err
			}
		}
		if !archive {
			if err := ensureChannelPrimary(tx, target.ContactID, target.Kind); err != nil {
				return err
			}
			var err error
			result, err = orm.GetTx[models.Channel](tx, result.ID)
			return err
		}
		return nil
	})
	if err != nil {
		return channelError(err)
	}
	if archive {
		return engine.NoContent()
	}
	body := map[string]any{
		"id":                 result.ID,
		"tenant_id":          result.TenantID,
		"created_by":         result.CreatedBy,
		"deleted_at":         result.DeletedAt,
		"contact_id":         result.ContactID,
		"kind":               result.Kind,
		"value":              result.Value,
		"label":              result.Label,
		"is_primary":         result.IsPrimary,
		"whatsapp":           result.Whatsapp,
		"marketing_consent":  result.MarketingConsent,
		"consent_updated_at": result.ConsentUpdatedAt,
		"consent_source":     result.ConsentSource,
		"created_at":         result.CreatedAt,
		"updated_at":         result.UpdatedAt,
		"etag":               result.Etag,
	}
	if id == "" {
		return engine.Created(body)
	}
	return engine.OK(body)
}

func channelGroup(contactID string, kind models.ChannelKind) orm.Condition[models.Channel] {
	return models.ChannelFields.ContactID.Eq(contactID).And(models.ChannelFields.Kind.Eq(kind))
}

func channelPrimary(tx *db.Tx, contactID string, kind models.ChannelKind) (string, error) {
	row, err := orm.From[models.Channel]().Tx(tx).
		Where(channelGroup(contactID, kind).And(models.ChannelFields.IsPrimary.Eq(true))).
		Select(models.ChannelFields.ID).One()
	if errors.Is(err, orm.ErrNotFound) {
		return "", nil
	}
	return row.ID, err
}

func ensureChannelPrimary(tx *db.Tx, contactID string, kind models.ChannelKind) error {
	primary, err := channelPrimary(tx, contactID, kind)
	if err != nil || primary != "" {
		return err
	}
	first, err := orm.From[models.Channel]().Tx(tx).Where(channelGroup(contactID, kind)).
		Select(models.ChannelFields.ID, models.ChannelFields.CreatedAt).
		OrderBy(models.ChannelFields.CreatedAt, false).One()
	if errors.Is(err, orm.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	first, err = orm.From[models.Channel]().Tx(tx).
		Where(channelGroup(contactID, kind).And(models.ChannelFields.CreatedAt.Eq(first.CreatedAt))).
		Select(models.ChannelFields.ID).OrderBy(models.ChannelFields.ID, false).One()
	if err != nil {
		return err
	}
	return orm.WriteTx(tx, first.ID, orm.NewValues[models.Channel]().Set(models.ChannelFields.IsPrimary, true), nil)
}

func channelError(err error) *engine.Response {
	if errors.Is(err, orm.ErrNotFound) {
		return engine.NotFound("contacts.channel.not_found", nil)
	}
	if host, ok := errors.AsType[*abi.HostError](err); ok {
		switch host.Code {
		case abi.ErrCodeNotFound:
			return engine.NotFound("contacts.channel.not_found", nil)
		case abi.ErrCodeValidationFailed, abi.ErrCodeFieldUnknown, abi.ErrCodeFieldNotWritable:
			return engine.Error(400, "contacts.channel.validation_failed", host.Message, host.Details)
		case abi.ErrCodeFieldWriteDenied, abi.ErrCodeFieldReadDenied, abi.ErrCodeCapabilityDenied:
			return engine.Forbidden("contacts.channel.forbidden", nil)
		case abi.ErrCodeEtagMismatch, abi.ErrCodeUniqueViolation, abi.ErrCodeForeignKeyViolation:
			return engine.Conflict("contacts.channel.conflict", host.Details)
		}
	}
	return engine.Error(500, "contacts.channel.failed", "Could not save the channel", nil)
}
