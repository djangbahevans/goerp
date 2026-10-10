package def

import (
	"fmt"
	"strings"
	"time"

	"github.com/djangbahevans/goerp/sdk/go/declare"
)

// KindRef identifies config read dependencies in the declaration registry.
const KindRef = "config_ref"

// RefDeclaration carries a reference's full key and expected manifest type.
type RefDeclaration struct {
	Key  string `json:"key"`
	Type string `json:"type"`
}

// ReadOnly reads configuration under the calling tenant's invocation context.
// Declare handles at package scope, but read their values inside handlers.
type ReadOnly[T any] struct {
	value Value[T]
}

// Get returns the resolved value, or the zero value when Lookup reports false.
// It panics without a linked config host or when the key is undeclared.
func (r ReadOnly[T]) Get() T { return r.value.Get() }

// Lookup reports whether the host resolves a value that decodes into T.
// An unloaded soft dependency, host failure or decoding failure returns the
// zero value and false. It panics without a linked config host or for an undeclared key.
func (r ReadOnly[T]) Lookup() (T, bool) { return r.value.Lookup() }

// Ref declares a read dependency on another module's full "{module}.{key}" name.
// T maps to the matching config constructor's type for string, bool, int,
// float64, time.Duration, []string, []int and []float64; other types use JSON.
// It panics for an invalid name or a platform key; use the company handles for those.
func Ref[T any](name string) ReadOnly[T] {
	owner, key, ok := strings.Cut(name, ".")
	if !ok || !keyPattern.MatchString(owner) || !keyPattern.MatchString(key) || owner == "company" {
		panic(fmt.Sprintf("config.Ref: %q must be another module's {module}.{key} name; use the company handles for platform keys", name))
	}

	r := reference[T](name)
	declare.Add(KindRef, RefDeclaration{Key: name, Type: r.value.typ})

	return r
}

var (
	// CompanyName reads the calling tenant's company name without a declaration.
	CompanyName = reference[string]("company.name")
	// CompanyAddress reads the calling tenant's address; an unset field reports false from Lookup.
	CompanyAddress = reference[string]("company.address")
	// CompanyTaxID reads the calling tenant's tax ID; an unset field reports false from Lookup.
	CompanyTaxID = reference[string]("company.tax_id")
	// CompanyLogoURL reads the calling tenant's logo URL; an unset field reports false from Lookup.
	CompanyLogoURL = reference[string]("company.logo_url")
)

func reference[T any](key string) ReadOnly[T] {
	var zero T
	switch any(zero).(type) {
	case string:
		return newReadOnly[T](key, TypeString, decodeString)
	case bool:
		return newReadOnly[T](key, TypeBoolean, decodeBool)
	case int:
		return newReadOnly[T](key, TypeInteger, decodeInt)
	case float64:
		return newReadOnly[T](key, TypeFloat, decodeFloat)
	case time.Duration:
		return newReadOnly[T](key, TypeDuration, decodeDuration)
	case []string:
		return newReadOnly[T](key, TypeStringList, decodeSlice(decodeString))
	case []int:
		return newReadOnly[T](key, TypeIntegerList, decodeSlice(decodeInt))
	case []float64:
		return newReadOnly[T](key, TypeFloatList, decodeSlice(decodeFloat))
	default:
		return ReadOnly[T]{value: Value[T]{
			key:    key,
			typ:    TypeJSON,
			decode: decodeJSON[T],
		}}
	}
}

func newReadOnly[T, E any](key, typ string, decode func(any) (E, error)) ReadOnly[T] {
	return ReadOnly[T]{value: Value[T]{
		key: key,
		typ: typ,
		decode: func(raw any) (T, error) {
			value, err := decode(raw)
			if err != nil {
				var zero T
				return zero, err
			}

			// reference selects a scalar or slice decoder whose result type is exactly T.
			return any(value).(T), nil
		},
	}}
}
