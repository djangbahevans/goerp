package main

import (
	"cmp"
	"strings"

	"github.com/djangbahevans/goerp/modules/contacts/models"
	"github.com/djangbahevans/goerp/sdk/go/orm"
)

func init() {
	orm.RegisterComputed("_compute_full_name", computeFullName)
	orm.RegisterComputed("_compute_display_name", computeDisplayName)
	orm.RegisterComputed("_compute_sort_name", computeSortName)
	orm.RegisterComputed("_compute_channel_value", computeChannelValue)
	orm.RegisterConstraint[models.Contact](orm.OnCreate, checkContact)
	orm.RegisterConstraint[models.Contact](orm.OnWrite, checkContact)
}

// Contact events require declared sources for their optional channel values.
func computeChannelValue(_ orm.ComputeContext, _ map[string]any) (any, error) {
	return nil, nil
}

func computeFullName(_ orm.ComputeContext, record map[string]any) (any, error) {
	if stringValue(record, "type") == "organization" {
		return stringValue(record, "legal_name"), nil
	}
	return joinName(stringValue(record, "given_name"), stringValue(record, "other_names"), stringValue(record, "family_name")), nil
}

func computeDisplayName(_ orm.ComputeContext, record map[string]any) (any, error) {
	return contactDisplayName(record), nil
}

func contactDisplayName(record map[string]any) string {
	if stringValue(record, "type") == "organization" {
		return cmp.Or(stringValue(record, "trading_name"), stringValue(record, "legal_name"))
	}
	return joinName(cmp.Or(stringValue(record, "preferred_name"), stringValue(record, "given_name")), stringValue(record, "family_name"))
}

func computeSortName(_ orm.ComputeContext, record map[string]any) (any, error) {
	if stringValue(record, "type") == "organization" {
		return contactDisplayName(record), nil
	}
	family := stringValue(record, "family_name")
	given := joinName(stringValue(record, "given_name"), stringValue(record, "other_names"))
	if family != "" && given != "" {
		return family + ", " + given, nil
	}
	return cmp.Or(family, given), nil
}

func checkContact(_ orm.ConstraintContext, record map[string]any) *orm.ConstraintResult {
	var forbidden []string
	switch stringValue(record, "type") {
	case "person":
		if stringValue(record, "given_name") == "" && stringValue(record, "family_name") == "" {
			return orm.Reject("given_name", "a person needs a given name or family name")
		}
		forbidden = []string{"legal_name", "trading_name"}
	case "organization":
		if stringValue(record, "legal_name") == "" {
			return orm.Reject("legal_name", "an organization needs a legal name")
		}
		forbidden = []string{"title", "given_name", "other_names", "family_name", "preferred_name", "date_of_birth", "gender"}
	default:
		return orm.Reject("type", "choose person or organization")
	}

	for _, field := range forbidden {
		if value := record[field]; value != nil && value != "" {
			return orm.Reject(field, "this field does not apply to this contact type")
		}
	}
	return orm.Allow()
}

func joinName(parts ...string) string {
	nonempty := make([]string, 0, len(parts))
	for _, part := range parts {
		if part != "" {
			nonempty = append(nonempty, part)
		}
	}
	return strings.Join(nonempty, " ")
}

func stringValue(record map[string]any, key string) string {
	s, _ := record[key].(string)
	return strings.TrimSpace(s)
}
