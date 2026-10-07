package main

import (
	"fmt"

	"github.com/djangbahevans/goerp/modules/contacts/models"
	"github.com/djangbahevans/goerp/sdk/go/db"
	"github.com/djangbahevans/goerp/sdk/go/orm"
)

func init() {
	orm.RegisterComputed("_compute_display_name", computeDisplayName)
	orm.RegisterConstraint("contacts.contact", orm.OnCreate, checkContactCreate)
	orm.RegisterConstraint("contacts.contact", orm.OnWrite, checkContactWrite)
}

func computeDisplayName(_ orm.ComputeContext, record map[string]any) (any, error) {
	name := stringValue(record, "name")
	companyID := stringValue(record, "company_id")
	if companyID == "" || stringValue(record, "type") != string(models.ContactTypePerson) {
		return name, nil
	}
	title, err := companyName(companyID)
	if err != nil {
		return nil, fmt.Errorf("load company %s: %w", companyID, err)
	}
	return name + " (" + title + ")", nil
}

// companyName reads through orm so a rename in the same transaction is visible;
// raw SQL covers archived companies, which orm reads hide but retained links
// still display.
func companyName(id string) (string, error) {
	company, err := orm.Get[models.Contact](id, models.ContactFields.Name)
	if err == nil {
		return company.Name, nil
	}
	if !orm.IsNotFound(err) {
		return "", err
	}
	archived, err := db.QueryOne[struct {
		Name string `db:"name"`
	}]("SELECT name FROM contacts WHERE id = $1", []any{id})
	return archived.Name, err
}

func checkContactCreate(_ orm.ConstraintContext, record map[string]any) *orm.ConstraintResult {
	return checkCompanyLink(record, "")
}

func checkContactWrite(_ orm.ConstraintContext, record map[string]any) *orm.ConstraintResult {
	id := stringValue(record, "id")
	existing, err := orm.Get[models.Contact](id)
	if err != nil {
		if orm.IsNotFound(err) {
			return orm.Allow()
		}
		return orm.Reject("id", "could not load the existing contact: "+err.Error())
	}

	if existing.Type == models.ContactTypeCompany && stringValue(record, "type") == string(models.ContactTypePerson) {
		people, err := orm.Count(models.ContactFields.CompanyID.Eq(id))
		if err != nil {
			return orm.Reject("type", "could not check the company's people: "+err.Error())
		}
		if people > 0 {
			return orm.Reject("type", "a company that people belong to cannot become a person")
		}
	}

	previousCompanyID := ""
	if existing.CompanyID != nil {
		previousCompanyID = *existing.CompanyID
	}
	return checkCompanyLink(record, previousCompanyID)
}

// checkCompanyLink validates record's company_id. A link already stored in
// previousCompanyID is retained even if its company has since been archived.
func checkCompanyLink(record map[string]any, previousCompanyID string) *orm.ConstraintResult {
	companyID := stringValue(record, "company_id")
	if companyID == "" {
		return orm.Allow()
	}
	if stringValue(record, "type") != string(models.ContactTypePerson) {
		return orm.Reject("company_id", "only a person can belong to a company")
	}
	if companyID == stringValue(record, "id") {
		return orm.Reject("company_id", "a contact cannot belong to itself")
	}
	if companyID == previousCompanyID {
		return orm.Allow()
	}

	company, err := orm.Get[models.Contact](companyID)
	if err != nil && !orm.IsNotFound(err) {
		return orm.Reject("company_id", "could not load the company: "+err.Error())
	}
	if err != nil || company.Type != models.ContactTypeCompany || !company.IsActive {
		return orm.Reject("company_id", "must reference an active company")
	}
	return orm.Allow()
}

func stringValue(record map[string]any, key string) string {
	s, _ := record[key].(string)
	return s
}
