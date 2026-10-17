package main

import (
	"errors"
	"regexp"
	"strings"

	"github.com/djangbahevans/goerp/modules/contacts/models"
	"github.com/djangbahevans/goerp/modules/contacts/schema"
	"github.com/djangbahevans/goerp/sdk/go/authz"
	"github.com/djangbahevans/goerp/sdk/go/orm"
)

func init() {
	orm.RegisterComputed("_compute_tax_id", computeTaxID)
	orm.RegisterComputed("_compute_identifier_unique_key", computeIdentifierUniqueKey)
	orm.RegisterComputed("_compute_identifier_is_tax", computeIdentifierIsTax)
	orm.RegisterComputed("_compute_identifier_is_sensitive", computeIdentifierIsSensitive)
	orm.RegisterConstraint[models.IdentifierType](orm.OnCreate, checkIdentifierType)
	orm.RegisterConstraint[models.IdentifierType](orm.OnWrite, checkIdentifierType)
	orm.RegisterConstraint[models.IdentifierType](orm.OnDelete, checkIdentifierTypeDelete)
	orm.RegisterConstraint[models.Identifier](orm.OnCreate, checkIdentifier)
	orm.RegisterConstraint[models.Identifier](orm.OnWrite, checkIdentifier)
	orm.RegisterConstraint[models.Identifier](orm.OnDelete, checkIdentifierDelete)
}

func identifierType(record map[string]any) (models.IdentifierType, error) {
	return orm.Get[models.IdentifierType](stringValue(record, "type_id"))
}

func computeIdentifierUniqueKey(_ orm.ComputeContext, record map[string]any) (any, error) {
	typ, err := identifierType(record)
	if err != nil || !typ.IsUnique {
		return nil, err
	}
	return stringValue(record, "value"), nil
}

func computeIdentifierIsTax(_ orm.ComputeContext, record map[string]any) (any, error) {
	typ, err := identifierType(record)
	return typ.IsTax, err
}

func computeIdentifierIsSensitive(_ orm.ComputeContext, record map[string]any) (any, error) {
	typ, err := identifierType(record)
	return typ.IsSensitive, err
}

func computeTaxID(_ orm.ComputeContext, record map[string]any) (any, error) {
	identifier, err := orm.From[models.Identifier]().Where(
		models.IdentifierFields.ContactID.Eq(stringValue(record, "id")).
			And(models.IdentifierFields.IsTax.Eq(true)),
	).Select(models.IdentifierFields.Value).One()
	if errors.Is(err, orm.ErrNotFound) {
		return nil, nil
	}
	return identifier.Value, err
}

func checkIdentifierType(_ orm.ConstraintContext, record map[string]any) *orm.ConstraintResult {
	if stringValue(record, "code") == "" {
		return orm.Reject("code", "enter an identifier type code")
	}
	if stringValue(record, "label") == "" {
		return orm.Reject("label", "enter an identifier type label")
	}
	if record["is_tax"] == true && record["is_sensitive"] == true {
		return orm.Reject("is_sensitive", "a tax identifier type cannot be sensitive")
	}
	pattern, _ := record["pattern"].(string)
	if _, err := regexp.Compile(pattern); err != nil {
		return orm.Reject("pattern", "enter a valid RE2 pattern")
	}
	return orm.Allow()
}

func checkIdentifierTypeDelete(_ orm.ConstraintContext, record map[string]any) *orm.ConstraintResult {
	if stringValue(record, "owner_module") != "" {
		return orm.Reject("owner_module", "module-owned identifier types cannot be archived")
	}
	return orm.Allow()
}

func normalizeIdentifier(value string, typ models.IdentifierType) string {
	value = strings.TrimSpace(value)
	if typ.Uppercase {
		value = strings.ToUpper(value)
	}
	return value
}

func identifierValueError(value string, typ models.IdentifierType) string {
	if value == "" {
		return "enter an identifier value"
	}
	if typ.Pattern != nil && *typ.Pattern != "" {
		pattern, err := regexp.Compile("\\A(?:" + *typ.Pattern + ")\\z")
		if err != nil || !pattern.MatchString(value) {
			return "the value must fully match the identifier type's pattern"
		}
	}
	return ""
}

func checkIdentifier(_ orm.ConstraintContext, record map[string]any) *orm.ConstraintResult {
	typ, err := identifierType(record)
	if err != nil {
		return orm.Reject("type_id", "select an available identifier type")
	}
	value := stringValue(record, "value")
	if original, _ := record["value"].(string); original != normalizeIdentifier(original, typ) {
		return orm.Reject("value", "the identifier value must be normalized for its type")
	}
	if message := identifierValueError(value, typ); message != "" {
		return orm.Reject("value", message)
	}
	contact, err := orm.Get[models.Contact](stringValue(record, "contact_id"))
	if err != nil || (typ.AppliesTo != "any" && string(typ.AppliesTo) != string(contact.Type)) {
		return orm.Reject("contact_id", "the identifier type does not apply to this contact")
	}
	return checkIdentifierDelete(orm.ConstraintContext{}, record)
}

func checkIdentifierDelete(_ orm.ConstraintContext, record map[string]any) *orm.ConstraintResult {
	if record["is_sensitive"] == true {
		allowed, err := authz.Check(schema.PersonalWrite, "")
		if err != nil || !allowed {
			return orm.Reject("type_id", "editing sensitive identifiers requires personal write permission")
		}
	}
	return orm.Allow()
}
