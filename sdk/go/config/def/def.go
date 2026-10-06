// Package def holds the host-call-free half of the config SDK: typed config
// definitions and their options. A module's schema package imports it to
// name a config key without linking host functions (go-sdk-reference.md §14
// "Defining a config key", §22 "Package layout"). The Value read and write
// methods delegate to a Host that sdk/go/config installs when it is linked.
package def

import (
	"encoding/json/v2"
	"errors"
	"fmt"
	"reflect"
	"regexp"
	"time"

	abi "github.com/djangbahevans/goerp/contract/abi/v1"
)

// Manifest config_schema types (manifest-spec.md §17).
const (
	TypeString      = "string"
	TypeBoolean     = "boolean"
	TypeInteger     = "integer"
	TypeFloat       = "float"
	TypeDuration    = "duration"
	TypeStringList  = "string[]"
	TypeIntegerList = "integer[]"
	TypeFloatList   = "float[]"
	TypeJSON        = "json"
)

// ErrNoHost is returned by Value.Set when no Host is installed, i.e.
// sdk/go/config is not linked into the binary.
var ErrNoHost = errors.New("sdk/go/config/def: no host installed; import sdk/go/config to read and write config")

var keyPattern = regexp.MustCompile(`^[A-Za-z0-9_]+$`)

// Host performs the host.config calls behind Value's methods. Keys are the
// short keys of the calling module's own definitions.
type Host interface {
	Get(key string) (value any, found bool, err error)
	Set(key string, value any) error
}

var host Host

// SetHost installs the Host the Value methods delegate to. It is called from
// sdk/go/config's init.
func SetHost(h Host) { host = h }

// Choice is one static option of a select or multiselect config key.
type Choice struct {
	Value string
	Label string
}

// Spec is the settings-UI metadata and constraints a definition carries.
// Min, Max, MinDuration and MaxDuration are nil when unset.
type Spec struct {
	Label           string
	Description     string
	Category        string
	FieldType       string
	Choices         []Choice
	Min             *float64
	Max             *float64
	MinDuration     *time.Duration
	MaxDuration     *time.Duration
	Pattern         string
	Required        bool
	Public          bool
	RestartRequired bool
	Encrypted       bool
	Generated       bool
}

// Option configures a definition.
type Option func(*Spec)

// Label sets the key's label in the settings UI. It is required.
func Label(text string) Option { return func(s *Spec) { s.Label = text } }

// Description sets the key's help text.
func Description(text string) Option { return func(s *Spec) { s.Description = text } }

// Category sets the settings page grouping label.
func Category(label string) Option { return func(s *Spec) { s.Category = label } }

// FieldType overrides the UI input type derived from the value type.
func FieldType(fieldType string) Option { return func(s *Spec) { s.FieldType = fieldType } }

// Choices sets the static options of a select or multiselect key.
func Choices(choices ...Choice) Option { return func(s *Spec) { s.Choices = choices } }

// Min sets the minimum of a numeric value.
func Min(n float64) Option { return func(s *Spec) { s.Min = &n } }

// Max sets the maximum of a numeric value.
func Max(n float64) Option { return func(s *Spec) { s.Max = &n } }

// MinDuration sets the minimum of a Duration value.
func MinDuration(d time.Duration) Option { return func(s *Spec) { s.MinDuration = &d } }

// MaxDuration sets the maximum of a Duration value.
func MaxDuration(d time.Duration) Option { return func(s *Spec) { s.MaxDuration = &d } }

// Pattern sets the regular expression a string value must match.
func Pattern(regex string) Option { return func(s *Spec) { s.Pattern = regex } }

// Required marks the key as one the module will not start without. The
// definition's default must be the zero value.
func Required() Option { return func(s *Spec) { s.Required = true } }

// Public includes the key in /_meta/schema for the frontend.
func Public() Option { return func(s *Spec) { s.Public = true } }

// RestartRequired marks a change to the key as needing a module hot-reload.
func RestartRequired() Option { return func(s *Spec) { s.RestartRequired = true } }

// Encrypted stores the value encrypted at rest; reads return the plaintext.
func Encrypted() Option { return func(s *Spec) { s.Encrypted = true } }

// Generated marks the key as provisioned by module code, never typed by an
// admin.
func Generated() Option { return func(s *Spec) { s.Generated = true } }

// Value is a typed config definition binding a key, its manifest type, its
// single default and its settings metadata to the Go value type T.
type Value[T any] struct {
	key    string
	typ    string
	def    T
	spec   Spec
	decode func(any) (T, error)
	encode func(T) (any, error)
}

// Definition is the value-type-erased view of a Value, for APIs that take a
// definition of any type.
type Definition interface {
	Key() string
	Type() string
	Default() any
	Spec() Spec
}

func define[T any](key, typ string, def T, opts []Option, decode func(any) (T, error), encode func(T) (any, error)) Value[T] {
	var spec Spec
	for _, opt := range opts {
		opt(&spec)
	}

	switch {
	case !keyPattern.MatchString(key):
		panic(fmt.Sprintf("config: key %q must be non-empty alphanumerics and underscores, with no module prefix", key))
	case spec.Label == "":
		panic(fmt.Sprintf("config key %q needs config.Label", key))
	case spec.Required && !isZero(def):
		panic(fmt.Sprintf("config key %q is Required, so its default must be the zero value", key))
	}
	if spec.Pattern != "" {
		if _, err := regexp.Compile(spec.Pattern); err != nil {
			panic(fmt.Sprintf("config key %q has an invalid Pattern: %v", key, err))
		}
	}

	return Value[T]{key: key, typ: typ, def: def, spec: spec, decode: decode, encode: encode}
}

func isZero[T any](v T) bool {
	rv := reflect.ValueOf(&v).Elem()
	if rv.Kind() == reflect.Slice {
		return rv.Len() == 0
	}
	return rv.IsZero()
}

// Key returns the short key, without the module prefix.
func (v Value[T]) Key() string { return v.key }

// Type returns the manifest config_schema type.
func (v Value[T]) Type() string { return v.typ }

// Default returns the definition's default.
func (v Value[T]) Default() any { return v.def }

// Spec returns the definition's settings metadata.
func (v Value[T]) Spec() Spec { return v.spec }

// Get returns the tenant's value, or the definition's default when the
// tenant has set none, the stored value does not decode into T, or the host
// call fails. It panics when no Host is installed, since a read that silently
// returned the default would hide a module that never imports sdk/go/config,
// and when the host reports the key as undeclared in the module's manifest.
func (v Value[T]) Get() T {
	if val, ok := v.Lookup(); ok {
		return val
	}
	return v.def
}

// Lookup returns the tenant's value and whether one is set. It reports false
// for a stored value that does not decode into T and when the host call
// fails. It panics when no Host is installed or the key is undeclared.
func (v Value[T]) Lookup() (T, bool) {
	var zero T
	if host == nil {
		panic(ErrNoHost)
	}
	raw, found, err := host.Get(v.key)
	if hostErr, ok := errors.AsType[*abi.HostError](err); ok && hostErr.Code == abi.ErrCodeConfigKeyUndeclared {
		panic(fmt.Sprintf("config key %q is not declared in the module's config_schema; regenerate the manifest", v.key))
	}
	if err != nil || !found {
		return zero, false
	}
	val, err := v.decode(raw)
	if err != nil {
		return zero, false
	}
	return val, true
}

// Set writes the tenant's value. The host rejects a key the calling module
// does not own.
func (v Value[T]) Set(val T) error {
	if host == nil {
		return ErrNoHost
	}
	raw, err := v.encode(val)
	if err != nil {
		return fmt.Errorf("encode config %s: %w", v.key, err)
	}
	return host.Set(v.key, raw)
}

// String declares a string key.
func String(key, def string, opts ...Option) Value[string] {
	return define(key, TypeString, def, opts, decodeString, identity[string])
}

// Bool declares a boolean key.
func Bool(key string, def bool, opts ...Option) Value[bool] {
	return define(key, TypeBoolean, def, opts, decodeBool, identity[bool])
}

// Int declares an integer key.
func Int(key string, def int, opts ...Option) Value[int] {
	return define(key, TypeInteger, def, opts, decodeInt, encodeInt)
}

// Float declares a float key.
func Float(key string, def float64, opts ...Option) Value[float64] {
	return define(key, TypeFloat, def, opts, decodeFloat, identity[float64])
}

// Duration declares a duration key, stored as a Go duration string ("15m").
func Duration(key string, def time.Duration, opts ...Option) Value[time.Duration] {
	return define(key, TypeDuration, def, opts, decodeDuration, encodeDuration)
}

// StringSlice declares a "string[]" key.
func StringSlice(key string, def []string, opts ...Option) Value[[]string] {
	return define(key, TypeStringList, def, opts, decodeSlice(decodeString), encodeSlice(identity[string]))
}

// IntSlice declares an "integer[]" key.
func IntSlice(key string, def []int, opts ...Option) Value[[]int] {
	return define(key, TypeIntegerList, def, opts, decodeSlice(decodeInt), encodeSlice(encodeInt))
}

// FloatSlice declares a "float[]" key.
func FloatSlice(key string, def []float64, opts ...Option) Value[[]float64] {
	return define(key, TypeFloatList, def, opts, decodeSlice(decodeFloat), encodeSlice(identity[float64]))
}

// JSON declares a "json" key whose stored JSON decodes into T.
func JSON[T any](key string, def T, opts ...Option) Value[T] {
	return define(key, TypeJSON, def, opts, decodeJSON[T], encodeJSON[T])
}

func identity[T any](v T) (any, error) { return v, nil }

func decodeString(v any) (string, error) {
	s, ok := v.(string)
	if !ok {
		return "", fmt.Errorf("%T is not a string", v)
	}
	return s, nil
}

func decodeBool(v any) (bool, error) {
	b, ok := v.(bool)
	if !ok {
		return false, fmt.Errorf("%T is not a boolean", v)
	}
	return b, nil
}

// decodeInt and decodeFloat accept either numeric type, since a msgpack
// decoder yields int64 or float64 depending on how the host encoded the value.
func decodeInt(v any) (int, error) {
	switch n := v.(type) {
	case int64:
		return int(n), nil
	case float64:
		return int(n), nil
	default:
		return 0, fmt.Errorf("%T is not a number", v)
	}
}

func decodeFloat(v any) (float64, error) {
	switch n := v.(type) {
	case float64:
		return n, nil
	case int64:
		return float64(n), nil
	default:
		return 0, fmt.Errorf("%T is not a number", v)
	}
}

func decodeDuration(v any) (time.Duration, error) {
	s, err := decodeString(v)
	if err != nil {
		return 0, err
	}
	return time.ParseDuration(s)
}

func decodeSlice[E any](decodeElem func(any) (E, error)) func(any) ([]E, error) {
	return func(v any) ([]E, error) {
		items, ok := v.([]any)
		if !ok {
			return nil, fmt.Errorf("%T is not an array", v)
		}
		out := make([]E, len(items))
		for i, item := range items {
			elem, err := decodeElem(item)
			if err != nil {
				return nil, fmt.Errorf("element %d: %w", i, err)
			}
			out[i] = elem
		}
		return out, nil
	}
}

func decodeJSON[T any](v any) (T, error) {
	var out T
	data, err := json.Marshal(v)
	if err != nil {
		return out, err
	}
	err = json.Unmarshal(data, &out)
	return out, err
}

func encodeInt(n int) (any, error) { return int64(n), nil }

func encodeDuration(d time.Duration) (any, error) { return d.String(), nil }

func encodeSlice[E any](encodeElem func(E) (any, error)) func([]E) (any, error) {
	return func(items []E) (any, error) {
		out := make([]any, len(items))
		for i, item := range items {
			elem, err := encodeElem(item)
			if err != nil {
				return nil, err
			}
			out[i] = elem
		}
		return out, nil
	}
}

func encodeJSON[T any](v T) (any, error) {
	data, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	var out any
	err = json.Unmarshal(data, &out)
	return out, err
}
