package main

import (
	"errors"

	"github.com/djangbahevans/goerp/modules/contacts/models"
	"github.com/djangbahevans/goerp/sdk/go/orm"
)

func init() {
	orm.RegisterComputed("_compute_phone", computePhone)
	orm.RegisterComputed("_compute_email", computeEmail)
	orm.RegisterComputed("_compute_phone_digits", computePhoneDigits)
	orm.RegisterConstraint[models.Channel](orm.OnCreate, checkChannel)
	orm.RegisterConstraint[models.Channel](orm.OnWrite, checkChannel)
}

func primaryValue(record map[string]any, kind models.ChannelKind) (any, error) {
	id := stringValue(record, "id")
	if id == "" {
		return nil, nil
	}
	channel, err := orm.From[models.Channel]().Where(
		models.ChannelFields.ContactID.Eq(id).
			And(models.ChannelFields.Kind.Eq(kind)).
			And(models.ChannelFields.IsPrimary.Eq(true)),
	).Select(models.ChannelFields.Value).One()
	if errors.Is(err, orm.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return channel.Value, nil
}

func computePhone(_ orm.ComputeContext, record map[string]any) (any, error) {
	return primaryValue(record, models.ChannelKind("phone"))
}

func computeEmail(_ orm.ComputeContext, record map[string]any) (any, error) {
	return primaryValue(record, models.ChannelKind("email"))
}

func computePhoneDigits(_ orm.ComputeContext, record map[string]any) (any, error) {
	value, err := primaryValue(record, models.ChannelKind("phone"))
	if err != nil || value == nil {
		return nil, err
	}
	return phoneDigits(value.(string)), nil
}

func checkChannel(_ orm.ConstraintContext, record map[string]any) *orm.ConstraintResult {
	if stringValue(record, "value") == "" {
		return orm.Reject("value", "enter a phone number or email address")
	}
	if record["whatsapp"] == true && stringValue(record, "kind") != "phone" {
		return orm.Reject("whatsapp", "WhatsApp requires a phone channel")
	}
	return orm.Allow()
}
