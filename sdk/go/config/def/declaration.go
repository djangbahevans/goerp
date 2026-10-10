package def

import (
	"fmt"

	"github.com/djangbahevans/goerp/sdk/go/declare"
)

// KindConfig identifies owned config definitions in the declaration registry.
const KindConfig = "config"

// Declaration carries a short key, manifest type, encoded default and settings
// metadata. A required definition has a nil default.
type Declaration struct {
	Key             string   `json:"key"`
	Type            string   `json:"type"`
	Default         any      `json:"default"`
	Label           string   `json:"label"`
	Description     string   `json:"description,omitempty"`
	Category        string   `json:"category,omitempty"`
	FieldType       string   `json:"field_type,omitempty"`
	Choices         []Choice `json:"choices,omitempty"`
	Min             any      `json:"min,omitempty"`
	Max             any      `json:"max,omitempty"`
	Pattern         string   `json:"pattern,omitempty"`
	Required        bool     `json:"required,omitzero"`
	Public          bool     `json:"public,omitzero"`
	RestartRequired bool     `json:"restart_required,omitzero"`
	Encrypted       bool     `json:"encrypted,omitzero"`
	Generated       bool     `json:"generated,omitzero"`
}

func (v Value[T]) declare() {
	var defaultValue any
	if !v.spec.Required {
		encoded, err := v.encode(v.def)
		if err != nil {
			panic(fmt.Sprintf("config key %q: encode default: %v", v.key, err))
		}
		defaultValue = encoded
	}

	d := Declaration{
		Key:             v.key,
		Type:            v.typ,
		Default:         defaultValue,
		Label:           v.spec.Label,
		Description:     v.spec.Description,
		Category:        v.spec.Category,
		FieldType:       v.spec.FieldType,
		Choices:         v.spec.Choices,
		Pattern:         v.spec.Pattern,
		Required:        v.spec.Required,
		Public:          v.spec.Public,
		RestartRequired: v.spec.RestartRequired,
		Encrypted:       v.spec.Encrypted,
		Generated:       v.spec.Generated,
	}
	if v.spec.Min != nil {
		d.Min = *v.spec.Min
	}
	if v.spec.Max != nil {
		d.Max = *v.spec.Max
	}
	if v.spec.MinDuration != nil {
		d.Min = v.spec.MinDuration.String()
	}
	if v.spec.MaxDuration != nil {
		d.Max = v.spec.MaxDuration.String()
	}

	declare.Add(KindConfig, d)
}
