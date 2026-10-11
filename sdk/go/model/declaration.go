package model

import "encoding/json/v2"

// KindModel identifies model definitions in the declaration registry.
const KindModel = "model"

type modelDeclaration struct {
	model *ModelDeclaration
}

func (d modelDeclaration) MarshalJSON() ([]byte, error) {
	// Read the name at export time because ModelOption and fluent builders can mutate the definition.
	return json.Marshal(struct {
		Name string `json:"name"`
	}{Name: d.model.Name})
}
