// Package payloadfields lists the msgpack keys of a payload struct, following
// vmihailenco/msgpack's field rules, for the manifest generators that describe
// job and event payloads.
package payloadfields

import (
	"cmp"
	"encoding"
	"reflect"
	"slices"
	"strings"

	"github.com/vmihailenco/msgpack/v5"
)

// Field is one key of a payload struct as msgpack encodes it.
type Field struct {
	Key      string
	Type     reflect.Type
	Optional bool
}

// Of lists the keys of struct type t (or pointer to one), and reports whether
// t is a struct at all.
func Of(t reflect.Type) (fields []Field, isStruct bool) {
	t = Deref(t)
	if t.Kind() != reflect.Struct {
		return nil, false
	}
	return keys(t), true
}

// Deref strips pointers from t.
func Deref(t reflect.Type) reflect.Type {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	return t
}

// keys applies msgpack's rules: a tag's name, else the Go name; "-" and
// unexported fields left out; and an embedded struct inlined unless it is
// tagged noinline, encodes itself, or would shadow a key.
func keys(t reflect.Type) []Field {
	var fields []Field
	has := func(key string) bool { return slices.ContainsFunc(fields, func(f Field) bool { return f.Key == key }) }

	for f := range t.Fields() {
		tag, options, _ := strings.Cut(f.Tag.Get("msgpack"), ",")
		if tag == "-" || (!f.IsExported() && !f.Anonymous) {
			continue
		}
		optionList := strings.Split(options, ",")

		if f.Anonymous && !slices.Contains(optionList, "noinline") {
			if inner, ok := inlinedStruct(f.Type); ok {
				innerFields := keys(inner)
				if !slices.ContainsFunc(innerFields, func(i Field) bool { return has(i.Key) }) {
					fields = append(fields, innerFields...)
					continue
				}
			}
		}

		fields = append(fields, Field{
			Key:      cmp.Or(tag, f.Name),
			Type:     f.Type,
			Optional: f.Type.Kind() == reflect.Pointer || slices.Contains(optionList, "omitempty"),
		})
	}
	return fields
}

var selfEncoding = []reflect.Type{
	reflect.TypeFor[msgpack.CustomEncoder](),
	reflect.TypeFor[msgpack.Marshaler](),
	reflect.TypeFor[encoding.BinaryMarshaler](),
	reflect.TypeFor[encoding.TextMarshaler](),
}

// SelfEncoding reports whether t, or a pointer to it, encodes itself instead
// of as a struct of fields.
func SelfEncoding(t reflect.Type) bool {
	return slices.ContainsFunc(selfEncoding, func(i reflect.Type) bool {
		return t.Implements(i) || reflect.PointerTo(t).Implements(i)
	})
}

func inlinedStruct(t reflect.Type) (reflect.Type, bool) {
	t = Deref(t)
	if t.Kind() != reflect.Struct || SelfEncoding(t) {
		return nil, false
	}
	return t, true
}
