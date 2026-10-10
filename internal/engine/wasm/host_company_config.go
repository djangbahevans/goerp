package wasm

import (
	"context"

	abiv1 "github.com/djangbahevans/goerp/contract/abi/v1"
)

func (r *Runtime) readCompanyConfig(ctx context.Context, tenantID, key string) (abiv1.ConfigGetOutput, *abiv1.HostError) {
	switch key {
	case "company.name", "company.address", "company.tax_id", "company.logo_url":
	default:
		return abiv1.ConfigGetOutput{}, configKeyUndeclared(key)
	}

	if r.companyProfiles == nil {
		return abiv1.ConfigGetOutput{}, &abiv1.HostError{
			Code:    abiv1.ErrCodeUnavailable,
			Message: "no company profile store is configured",
		}
	}

	// Profile reads bypass module-config caches so each call reflects committed edits.
	profile, err := r.companyProfiles.GetProfile(ctx, tenantID)
	if err != nil {
		return abiv1.ConfigGetOutput{}, &abiv1.HostError{
			Code:    abiv1.ErrCodeUnavailable,
			Message: "could not load company profile",
			Retry:   true,
		}
	}

	var value *string
	switch key {
	case "company.name":
		value = &profile.Name
	case "company.address":
		value = profile.Address
	case "company.tax_id":
		value = profile.TaxID
	case "company.logo_url":
		value = profile.LogoURL
	}
	if value == nil {
		return abiv1.ConfigGetOutput{Found: false}, nil
	}

	return abiv1.ConfigGetOutput{Value: *value, Found: true}, nil
}
