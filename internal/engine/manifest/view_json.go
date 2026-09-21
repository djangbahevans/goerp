package manifest

import (
	"encoding/json/v2"
	"slices"
)

type viewAlias View

// viewRawMembers lists members that hold a different shape in a view type
// than the typed field they share a key with, so they bypass it.
var viewRawMembers = map[string][]string{
	"pivot": {"columns"},
}

func viewForceExtra(viewType string) func(name string) bool {
	return func(name string) bool { return slices.Contains(viewRawMembers[viewType], name) }
}

func (v *View) UnmarshalJSON(data []byte) error {
	members, err := decodeMembers(data)
	if err != nil {
		return err
	}

	var viewType string
	if raw, ok := members["type"]; ok {
		_ = json.Unmarshal(raw, &viewType)
	}

	var alias viewAlias
	if err := decodeSplitMembers(members, &alias, &alias.Extra, viewForceExtra(viewType)); err != nil {
		return err
	}
	*v = View(alias)

	return nil
}

func (v View) MarshalJSON() ([]byte, error) {
	return encodeWithExtra(viewAlias(v), v.Extra, viewForceExtra(v.Type))
}

type (
	listColumnAlias  ListColumn
	actionAlias      Action
	formFieldAlias   FormField
	formSectionAlias FormSection

	bulkActionAlias struct {
		actionAlias
		MinSelected int `json:"min_selected,omitzero"`
		MaxSelected int `json:"max_selected,omitzero"`
	}
)

func (c *ListColumn) UnmarshalJSON(data []byte) error {
	var alias listColumnAlias
	if err := decodeWithExtra(data, &alias, &alias.Extra); err != nil {
		return err
	}
	*c = ListColumn(alias)

	return nil
}

func (c ListColumn) MarshalJSON() ([]byte, error) {
	return encodeWithExtra(listColumnAlias(c), c.Extra, nil)
}

func (a *Action) UnmarshalJSON(data []byte) error {
	var alias actionAlias
	if err := decodeWithExtra(data, &alias, &alias.Extra); err != nil {
		return err
	}
	*a = Action(alias)

	return nil
}

func (a Action) MarshalJSON() ([]byte, error) {
	return encodeWithExtra(actionAlias(a), a.Extra, nil)
}

func (b *BulkAction) UnmarshalJSON(data []byte) error {
	var alias bulkActionAlias
	if err := decodeWithExtra(data, &alias, &alias.Extra); err != nil {
		return err
	}
	*b = BulkAction{Action: Action(alias.actionAlias), MinSelected: alias.MinSelected, MaxSelected: alias.MaxSelected}

	return nil
}

func (b BulkAction) MarshalJSON() ([]byte, error) {
	alias := bulkActionAlias{actionAlias: actionAlias(b.Action), MinSelected: b.MinSelected, MaxSelected: b.MaxSelected}

	return encodeWithExtra(alias, b.Extra, nil)
}

func (f *FormField) UnmarshalJSON(data []byte) error {
	var alias formFieldAlias
	if err := decodeWithExtra(data, &alias, &alias.Extra); err != nil {
		return err
	}
	*f = FormField(alias)

	return nil
}

func (f FormField) MarshalJSON() ([]byte, error) {
	return encodeWithExtra(formFieldAlias(f), f.Extra, nil)
}

func (s *FormSection) UnmarshalJSON(data []byte) error {
	var alias formSectionAlias
	if err := decodeWithExtra(data, &alias, &alias.Extra); err != nil {
		return err
	}
	*s = FormSection(alias)

	return nil
}

func (s FormSection) MarshalJSON() ([]byte, error) {
	return encodeWithExtra(formSectionAlias(s), s.Extra, nil)
}
