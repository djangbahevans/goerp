// Package configview builds the tenant admin view of a module's config_schema
// entries and the tenant's values, shared by the connector and module
// endpoints so both report a value the same way.
package configview

import (
	"encoding/json/jsontext"

	"github.com/djangbahevans/goerp/internal/engine/manifest"
	"github.com/djangbahevans/goerp/internal/engine/tenantconfig"
)

// Masked is what a read returns in place of a set encrypted value, and what a
// write treats as "unchanged".
const Masked = "***"

// Entry is one config_schema entry with the tenant's current value. Value is
// the stored value, else the default; for an encrypted entry it is Masked
// when set and null otherwise, never the plaintext.
type Entry struct {
	Key             string                 `json:"key"`
	Label           string                 `json:"label"`
	Description     string                 `json:"description,omitempty"`
	Type            string                 `json:"type"`
	FieldType       string                 `json:"field_type,omitempty"`
	Category        string                 `json:"category,omitempty"`
	Required        bool                   `json:"required"`
	Options         []manifest.FieldOption `json:"options,omitempty"`
	Min             any                    `json:"min,omitempty"`
	Max             any                    `json:"max,omitempty"`
	ValidationRegex string                 `json:"validation_regex,omitempty"`
	Encrypted       bool                   `json:"encrypted"`
	Generated       bool                   `json:"generated"`
	RestartRequired bool                   `json:"restart_required"`
	Default         any                    `json:"default"`
	IsSet           bool                   `json:"is_set"`
	Value           any                    `json:"value"`
}

// Entries returns one Entry per schema entry, in schema order, never nil.
func Entries(schema []manifest.ConfigEntry, rows map[string]tenantconfig.ModuleConfigRow) []Entry {
	entries := make([]Entry, 0, len(schema))
	for _, e := range schema {
		entries = append(entries, entry(e, rows))
	}
	return entries
}

func entry(e manifest.ConfigEntry, rows map[string]tenantconfig.ModuleConfigRow) Entry {
	row, set := rows[e.Key]
	v := Entry{
		Key: e.Key, Label: e.Label, Description: e.Description, Type: e.Type, FieldType: e.FieldType,
		Category: e.Category, Required: e.Required, Options: e.Options, Min: e.Min, Max: e.Max,
		ValidationRegex: e.ValidationRegex, Encrypted: e.Encrypted, Generated: e.Generated,
		RestartRequired: e.RestartRequired, Default: e.Default, IsSet: set,
	}
	switch {
	case e.Encrypted:
		if set {
			v.Value = Masked
		}
	case set:
		v.Value = jsontext.Value(row.Value)
	default:
		v.Value = e.Default
	}
	return v
}
