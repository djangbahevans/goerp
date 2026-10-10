// Package config provides typed tenant configuration handles. Value declares
// an owned key; Ref declares a read dependency on another module's key, and
// the company handles read the tenant's profile. Read values inside handlers
// under the invocation's tenant context. Importing config installs the host
// calls for handles declared through the host-call-free config/def package.
package config

import (
	"time"

	abi "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/djangbahevans/goerp/sdk/go/config/def"
	"github.com/djangbahevans/goerp/sdk/go/internal/hostcall"
)

func init() { def.SetHost(hostConfig{}) }

type hostConfig struct{}

func (hostConfig) Get(key string) (any, bool, error) {
	var out abi.ConfigGetOutput
	if err := hostcall.Do(hostConfigGet, abi.ConfigGetInput{Key: key}, &out); err != nil {
		return nil, false, err
	}
	return out.Value, out.Found, nil
}

func (hostConfig) Set(key string, value any) error {
	return hostcall.Do(hostConfigSet, abi.ConfigSetInput{Key: key, Value: value}, nil)
}

// Value is a typed config definition (see def.Value).
type Value[T any] = def.Value[T]

// ReadOnly reads a tenant value through Get and Lookup without write access.
type ReadOnly[T any] = def.ReadOnly[T]

// Ref declares a read dependency on another module's full "{module}.{key}" name.
// T maps string, bool, int, float64, time.Duration, []string, []int and []float64
// to their config types; other types use JSON. Invalid names and platform keys panic.
func Ref[T any](name string) ReadOnly[T] { return def.Ref[T](name) }

var (
	// CompanyName reads the calling tenant's company name without a declaration.
	CompanyName = def.CompanyName
	// CompanyAddress reads the tenant's address; Lookup reports false when unset.
	CompanyAddress = def.CompanyAddress
	// CompanyTaxID reads the tenant's tax ID; Lookup reports false when unset.
	CompanyTaxID = def.CompanyTaxID
	// CompanyLogoURL reads the tenant's logo URL; Lookup reports false when unset.
	CompanyLogoURL = def.CompanyLogoURL
)

// Definition is the value-type-erased view of a Value.
type Definition = def.Definition

// Option configures a definition.
type Option = def.Option

// Choice is one static option of a select or multiselect key.
type Choice = def.Choice

// String declares a string key.
func String(key, defaultValue string, opts ...Option) Value[string] {
	return def.String(key, defaultValue, opts...)
}

// Bool declares a boolean key.
func Bool(key string, defaultValue bool, opts ...Option) Value[bool] {
	return def.Bool(key, defaultValue, opts...)
}

// Int declares an integer key.
func Int(key string, defaultValue int, opts ...Option) Value[int] {
	return def.Int(key, defaultValue, opts...)
}

// Float declares a float key.
func Float(key string, defaultValue float64, opts ...Option) Value[float64] {
	return def.Float(key, defaultValue, opts...)
}

// Duration declares a duration key, stored as a Go duration string ("15m").
func Duration(key string, defaultValue time.Duration, opts ...Option) Value[time.Duration] {
	return def.Duration(key, defaultValue, opts...)
}

// StringSlice declares a "string[]" key.
func StringSlice(key string, defaultValue []string, opts ...Option) Value[[]string] {
	return def.StringSlice(key, defaultValue, opts...)
}

// IntSlice declares an "integer[]" key.
func IntSlice(key string, defaultValue []int, opts ...Option) Value[[]int] {
	return def.IntSlice(key, defaultValue, opts...)
}

// FloatSlice declares a "float[]" key.
func FloatSlice(key string, defaultValue []float64, opts ...Option) Value[[]float64] {
	return def.FloatSlice(key, defaultValue, opts...)
}

// JSON declares a "json" key whose stored JSON decodes into T.
func JSON[T any](key string, defaultValue T, opts ...Option) Value[T] {
	return def.JSON(key, defaultValue, opts...)
}

var (
	// Label sets the key's label in the settings UI. It is required.
	Label = def.Label
	// Description sets the key's help text.
	Description = def.Description
	// Category sets the settings page grouping label.
	Category = def.Category
	// FieldType overrides the UI input type derived from the value type.
	FieldType = def.FieldType
	// Choices sets the static options of a select or multiselect key.
	Choices = def.Choices
	// Min sets the minimum of a numeric value.
	Min = def.Min
	// Max sets the maximum of a numeric value.
	Max = def.Max
	// MinDuration sets the minimum of a Duration value.
	MinDuration = def.MinDuration
	// MaxDuration sets the maximum of a Duration value.
	MaxDuration = def.MaxDuration
	// Pattern sets the regular expression a string value must match.
	Pattern = def.Pattern
	// Required marks the key as one the module will not start without; its
	// default must be the zero value.
	Required = def.Required
	// Public includes the key in /_meta/schema for the frontend.
	Public = def.Public
	// RestartRequired marks a change as needing a module hot-reload.
	RestartRequired = def.RestartRequired
	// Encrypted stores the value encrypted at rest.
	Encrypted = def.Encrypted
	// Generated marks the key as provisioned by module code, never typed by
	// an admin.
	Generated = def.Generated
)
