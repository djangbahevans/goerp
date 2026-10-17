// Package addresses selects contact addresses for a business purpose.
package addresses

import "github.com/djangbahevans/goerp/modules/contacts/models"

// ForPurpose selects from a contact's complete address set. It prefers the
// primary address of purpose, then the earliest-created active address of that
// purpose, then the primary address. Creation ties use ID order. Archived
// addresses are ignored; no match returns false. Callers must read the set with
// the current caller's access rules before selection.
func ForPurpose(records []models.Address, purpose models.AddressType) (models.Address, bool) {
	var primary, matching *models.Address

	for i := range records {
		address := &records[i]
		if address.DeletedAt != nil {
			continue
		}

		if address.IsPrimary {
			primary = address
			if address.Type == purpose {
				return *address, true
			}
		}

		if address.Type == purpose && (matching == nil || address.CreatedAt.Before(matching.CreatedAt) ||
			(address.CreatedAt.Equal(matching.CreatedAt) && address.ID < matching.ID)) {
			matching = address
		}
	}

	if matching != nil {
		return *matching, true
	}
	if primary != nil {
		return *primary, true
	}
	return models.Address{}, false
}
