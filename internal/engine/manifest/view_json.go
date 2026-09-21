package manifest

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"reflect"
	"slices"
	"strings"
	"sync"
)

type viewAlias View

// viewRawMembers lists members that hold a different shape in a view type
// than the typed field they share a key with, so they bypass it.
var viewRawMembers = map[string][]string{
	"pivot": {"columns"},
}

// isViewExtraMember reports whether a member of a view of the given type
// is kept in View.Extra rather than in a typed field.
func isViewExtraMember(viewType, name string) bool {
	_, modeled := viewTypedMembers()[name]
	return !modeled || slices.Contains(viewRawMembers[viewType], name)
}

var viewTypedMembers = sync.OnceValue(func() map[string]struct{} {
	t := reflect.TypeFor[viewAlias]()
	names := make(map[string]struct{}, t.NumField())
	for f := range t.Fields() {
		if name, _, _ := strings.Cut(f.Tag.Get("json"), ","); name != "" && name != "-" {
			names[name] = struct{}{}
		}
	}
	return names
})

func (v *View) UnmarshalJSON(data []byte) error {
	var members map[string]jsontext.Value
	if err := json.Unmarshal(data, &members); err != nil {
		return err
	}

	var viewType string
	if raw, ok := members["type"]; ok {
		_ = json.Unmarshal(raw, &viewType)
	}

	typed := make(map[string]jsontext.Value, len(members))
	var extra map[string]jsontext.Value
	for name, value := range members {
		if !isViewExtraMember(viewType, name) {
			typed[name] = value
			continue
		}
		if extra == nil {
			extra = make(map[string]jsontext.Value)
		}
		extra[name] = value
	}

	encoded, err := json.Marshal(typed)
	if err != nil {
		return err
	}
	var alias viewAlias
	if err := json.Unmarshal(encoded, &alias); err != nil {
		return err
	}
	alias.Extra = extra
	*v = View(alias)

	return nil
}

func (v View) MarshalJSON() ([]byte, error) {
	encoded, err := json.Marshal(viewAlias(v))
	if err != nil {
		return nil, err
	}

	names := make([]string, 0, len(v.Extra))
	for name := range v.Extra {
		if isViewExtraMember(v.Type, name) {
			names = append(names, name)
		}
	}
	slices.Sort(names)

	// The typed encoding always carries name, type, resource and label, so
	// it is a non-empty object and each extra member follows a comma.
	out := encoded[:len(encoded)-1]
	for _, name := range names {
		out = append(out, ',')
		if out, err = jsontext.AppendQuote(out, name); err != nil {
			return nil, err
		}
		out = append(out, ':')
		out = append(out, v.Extra[name]...)
	}

	return append(out, '}'), nil
}
