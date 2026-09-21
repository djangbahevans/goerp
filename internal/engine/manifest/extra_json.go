package manifest

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"maps"
	"reflect"
	"slices"
	"strings"
	"sync"
)

var typedMemberNamesCache sync.Map

// typedMemberNames returns the JSON member names of t's typed fields,
// including those of embedded structs.
func typedMemberNames(t reflect.Type) map[string]struct{} {
	if cached, ok := typedMemberNamesCache.Load(t); ok {
		return cached.(map[string]struct{})
	}
	names := make(map[string]struct{}, t.NumField())
	for f := range t.Fields() {
		if f.Anonymous && f.Type.Kind() == reflect.Struct {
			maps.Copy(names, typedMemberNames(f.Type))
			continue
		}
		if name, _, _ := strings.Cut(f.Tag.Get("json"), ","); name != "" && name != "-" {
			names[name] = struct{}{}
		}
	}
	typedMemberNamesCache.Store(t, names)
	return names
}

func decodeMembers(data []byte) (map[string]jsontext.Value, error) {
	var members map[string]jsontext.Value
	if err := json.Unmarshal(data, &members); err != nil {
		return nil, err
	}
	return members, nil
}

// decodeSplitMembers decodes members into dst, which must not implement
// json.Unmarshaler, and stores the members it has no typed field for in
// extra. A member for which forceExtra reports true goes to extra even when
// dst has a typed field for it.
func decodeSplitMembers[T any](members map[string]jsontext.Value, dst *T, extra *map[string]jsontext.Value, forceExtra func(name string) bool) error {
	names := typedMemberNames(reflect.TypeFor[T]())
	typed := make(map[string]jsontext.Value, len(members))
	var unmodeled map[string]jsontext.Value
	for name, value := range members {
		if _, modeled := names[name]; modeled && (forceExtra == nil || !forceExtra(name)) {
			typed[name] = value
			continue
		}
		if unmodeled == nil {
			unmodeled = make(map[string]jsontext.Value)
		}
		unmodeled[name] = value
	}

	encoded, err := json.Marshal(typed)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(encoded, dst); err != nil {
		return err
	}
	*extra = unmodeled

	return nil
}

// decodeWithExtra decodes data into dst and stores the members dst has no
// typed field for in extra, which must point into dst.
func decodeWithExtra[T any](data []byte, dst *T, extra *map[string]jsontext.Value) error {
	members, err := decodeMembers(data)
	if err != nil {
		return err
	}
	return decodeSplitMembers(members, dst, extra, nil)
}

// encodeWithExtra encodes typed, then appends each extra member in name
// order. An extra member that typed already encodes is skipped, unless
// forceExtra reports true for it.
func encodeWithExtra[T any](typed T, extra map[string]jsontext.Value, forceExtra func(name string) bool) ([]byte, error) {
	encoded, err := json.Marshal(typed)
	if err != nil {
		return nil, err
	}

	names := typedMemberNames(reflect.TypeFor[T]())
	var appended []string
	for name := range extra {
		if _, modeled := names[name]; !modeled || (forceExtra != nil && forceExtra(name)) {
			appended = append(appended, name)
		}
	}
	slices.Sort(appended)

	out := encoded[:len(encoded)-1]
	needsComma := len(out) > 1
	for _, name := range appended {
		if needsComma {
			out = append(out, ',')
		}
		needsComma = true
		if out, err = jsontext.AppendQuote(out, name); err != nil {
			return nil, err
		}
		out = append(out, ':')
		out = append(out, extra[name]...)
	}

	return append(out, '}'), nil
}
