package main

import (
	"errors"

	"github.com/djangbahevans/goerp/modules/contacts/models"
	"github.com/djangbahevans/goerp/sdk/go/orm"
)

func init() {
	orm.RegisterComputed("_compute_city", computeCity)
	orm.RegisterComputed("_compute_country_code", computeCountryCode)
}

func primaryAddress(record map[string]any) (models.Address, error) {
	return orm.From[models.Address]().Where(
		models.AddressFields.ContactID.Eq(stringValue(record, "id")).
			And(models.AddressFields.IsPrimary.Eq(true)),
	).Select(models.AddressFields.City, models.AddressFields.CountryCode).One()
}

func computeCity(_ orm.ComputeContext, record map[string]any) (any, error) {
	address, err := primaryAddress(record)
	if errors.Is(err, orm.ErrNotFound) {
		return nil, nil
	}
	return address.City, err
}

func computeCountryCode(_ orm.ComputeContext, record map[string]any) (any, error) {
	address, err := primaryAddress(record)
	if errors.Is(err, orm.ErrNotFound) {
		return nil, nil
	}
	return address.CountryCode, err
}
