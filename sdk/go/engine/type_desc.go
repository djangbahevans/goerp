package engine

import (
	"cmp"
	"encoding"
	"encoding/json/v2"
	"reflect"
	"slices"
	"strings"
	"time"

	abi "github.com/djangbahevans/goerp/contract/abi/v1"
)

// Body declares that the route's JSON request body is a T. goerp codegen
// uses it to type the route's generated function; request handling ignores it.
func Body[T any]() CommonOption {
	return commonOptionFunc(func(c *routeConfig) { c.requestType = new(describeType(reflect.TypeFor[T]())) })
}

// Returns declares that the route's response data is a T, or for a list
// route that each item is a T. Like Body, it is used only by goerp codegen.
func Returns[T any]() CommonOption {
	return commonOptionFunc(func(c *routeConfig) { c.responseType = new(describeType(reflect.TypeFor[T]())) })
}

var (
	timeType            = reflect.TypeFor[time.Time]()
	byteType            = reflect.TypeFor[byte]()
	jsonMarshalerType   = reflect.TypeFor[json.Marshaler]()
	jsonMarshalerToType = reflect.TypeFor[json.MarshalerTo]()
	textMarshalerType   = reflect.TypeFor[encoding.TextMarshaler]()
)

// describeType describes t as encoding/json/v2 (Request.ParseJSON) encodes
// it. A type that refers back to itself is described as unknown where it
// recurs.
func describeType(t reflect.Type) TypeDesc {
	return describe(t, map[reflect.Type]bool{})
}

func describe(t reflect.Type, inProgress map[reflect.Type]bool) TypeDesc {
	switch t.Kind() {
	case reflect.Pointer, reflect.Slice, reflect.Array, reflect.Map, reflect.Struct:
		if inProgress[t] {
			return TypeDesc{Kind: abi.TypeKindUnknown}
		}
		inProgress[t] = true
		defer delete(inProgress, t)
	}

	if t.Kind() == reflect.Pointer {
		d := describe(t.Elem(), inProgress)
		d.Nullable = true
		return d
	}
	switch {
	case t == timeType:
		return TypeDesc{Kind: abi.TypeKindString}
	case implements(t, jsonMarshalerType), implements(t, jsonMarshalerToType):
		return TypeDesc{Kind: abi.TypeKindUnknown}
	case implements(t, textMarshalerType):
		return TypeDesc{Kind: abi.TypeKindString}
	}

	switch t.Kind() {
	case reflect.String:
		return TypeDesc{Kind: abi.TypeKindString}
	case reflect.Bool:
		return TypeDesc{Kind: abi.TypeKindBoolean}
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr,
		reflect.Float32, reflect.Float64:
		return TypeDesc{Kind: abi.TypeKindNumber}
	case reflect.Slice, reflect.Array:
		// A []byte or [N]byte is sent as a base64 string; a named byte type's
		// elements are sent as numbers.
		if t.Elem() == byteType {
			return TypeDesc{Kind: abi.TypeKindString}
		}
		return TypeDesc{Kind: abi.TypeKindArray, Elem: new(describe(t.Elem(), inProgress))}
	case reflect.Map:
		if !isObjectKey(t.Key()) {
			return TypeDesc{Kind: abi.TypeKindUnknown}
		}
		return TypeDesc{Kind: abi.TypeKindRecord, Elem: new(describe(t.Elem(), inProgress))}
	case reflect.Struct:
		return TypeDesc{Kind: abi.TypeKindObject, Name: t.Name(), Fields: describeFields(t, inProgress)}
	default:
		return TypeDesc{Kind: abi.TypeKindUnknown}
	}
}

// isObjectKey reports whether a map with keys of type k encodes as a JSON
// object: string, integer and TextMarshaler keys do.
func isObjectKey(k reflect.Type) bool {
	switch k.Kind() {
	case reflect.String,
		reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return true
	}
	return implements(k, textMarshalerType)
}

func implements(t, iface reflect.Type) bool {
	return t.Implements(iface) || reflect.PointerTo(t).Implements(iface)
}

type candidateField struct {
	desc   FieldDesc
	depth  int
	tagged bool
	seq    int // position in the encoder's field order
}

// describeFields lists t's JSON fields in the encoder's order, promoting an
// untagged embedded struct's fields and resolving name conflicts as the
// encoder does: the shallowest field wins, then the only tagged one, and
// otherwise the name is dropped.
func describeFields(t reflect.Type, inProgress map[reflect.Type]bool) []FieldDesc {
	var candidates []candidateField
	collectFields(t, 0, false, inProgress, &candidates)

	byName := make(map[string][]candidateField, len(candidates))
	for i, c := range candidates {
		c.seq = i
		byName[c.desc.Name] = append(byName[c.desc.Name], c)
	}

	winners := make([]candidateField, 0, len(byName))
	for _, named := range byName {
		if f, ok := dominantField(named); ok {
			winners = append(winners, f)
		}
	}
	slices.SortFunc(winners, func(a, b candidateField) int { return cmp.Compare(a.seq, b.seq) })

	fields := make([]FieldDesc, len(winners))
	for i, w := range winners {
		fields[i] = w.desc
	}
	return fields
}

func dominantField(fields []candidateField) (candidateField, bool) {
	minDepth := slices.MinFunc(fields, func(a, b candidateField) int { return cmp.Compare(a.depth, b.depth) }).depth
	fields = slices.DeleteFunc(slices.Clone(fields), func(f candidateField) bool { return f.depth > minDepth })
	if len(fields) == 1 {
		return fields[0], true
	}
	tagged := slices.DeleteFunc(fields, func(f candidateField) bool { return !f.tagged })
	if len(tagged) == 1 {
		return tagged[0], true
	}
	return candidateField{}, false
}

func collectFields(t reflect.Type, depth int, viaPointer bool, inProgress map[reflect.Type]bool, out *[]candidateField) {
	for sf := range t.Fields() {
		tag := sf.Tag.Get("json")
		if tag == "-" {
			continue
		}
		name, opts, _ := strings.Cut(tag, ",")

		if sf.Anonymous {
			ft := sf.Type
			embeddedPointer := ft.Kind() == reflect.Pointer
			if embeddedPointer {
				ft = ft.Elem()
			}
			if !sf.IsExported() && ft.Kind() != reflect.Struct {
				continue
			}
			if name == "" && ft.Kind() == reflect.Struct {
				if !inProgress[ft] {
					inProgress[ft] = true
					collectFields(ft, depth+1, viaPointer || embeddedPointer, inProgress, out)
					delete(inProgress, ft)
				}
				continue
			}
		} else if !sf.IsExported() {
			continue
		}

		desc := describe(sf.Type, inProgress)
		// ",string" sends a number as a JSON string.
		if hasTagOption(opts, "string") && desc.Kind == abi.TypeKindNumber {
			desc.Kind = abi.TypeKindString
		}
		*out = append(*out, candidateField{
			desc: FieldDesc{
				Name: cmp.Or(name, sf.Name),
				Type: desc,
				// A nil embedded pointer omits its promoted fields.
				Optional: viaPointer || hasTagOption(opts, "omitempty") || hasTagOption(opts, "omitzero"),
			},
			depth:  depth,
			tagged: name != "",
		})
	}
}

func hasTagOption(opts, option string) bool {
	for opt := range strings.SplitSeq(opts, ",") {
		if opt == option {
			return true
		}
	}
	return false
}
