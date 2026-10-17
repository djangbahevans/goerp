package main

import (
	"cmp"
	"errors"
	"strings"

	"github.com/djangbahevans/goerp/modules/contacts/models"
	"github.com/djangbahevans/goerp/sdk/go/db"
	"github.com/djangbahevans/goerp/sdk/go/orm"
)

func init() {
	orm.RegisterComputed("_compute_full_name", computeFullName)
	orm.RegisterComputed("_compute_display_name", computeDisplayName)
	orm.RegisterComputed("_compute_sort_name", computeSortName)
	orm.RegisterConstraint[models.Contact](orm.OnCreate, checkContact)
	orm.RegisterConstraint[models.Contact](orm.OnWrite, checkContact)
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
	id := stringValue(record, "id")
	if id != "" {
		invalid := models.AffiliationFields.PersonID.Eq(id)
		if stringValue(record, "type") == "person" {
			invalid = models.AffiliationFields.OrganizationID.Eq(id)
		}
		_, err := orm.From[models.Affiliation]().Where(invalid).Select(models.AffiliationFields.ID).One()
		if err == nil {
			return orm.Reject("type", "contact type conflicts with an affiliation")
		}
		if !errors.Is(err, orm.ErrNotFound) {
			return orm.Reject("type", "could not validate contact affiliations")
		}
		invalidRelationships, err := db.QueryOne[struct{ Count int }](
			`SELECT count(*) AS count FROM contact_relationships r
			 JOIN contact_relationship_types t ON t.id = r.type_id
			 WHERE r.contact_id = $1 AND r.deleted_at IS NULL AND
			 (CASE WHEN r.direction = 'forward' THEN t.from_type ELSE t.to_type END) <> $2`,
			[]any{id, stringValue(record, "type")})
		if err != nil || invalidRelationships.Count != 0 {
			return orm.Reject("type", "contact type conflicts with a relationship")
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
