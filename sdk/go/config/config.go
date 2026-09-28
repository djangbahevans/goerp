// Package config is sdk/go's outbound module-side caller for the
// host.config namespace (host-abi-reference.md §14) — typed getters and
// a setter over host.config.get/host.config.set, calling through
// sdk/go/internal/hostcall.
package config

import (
	"time"

	abi "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/djangbahevans/goerp/sdk/go/internal/hostcall"
)

// Get returns key's resolved value, or defaultValue if unset or on error.
func Get(key string, defaultValue any) any {
	var out abi.ConfigGetOutput
	if err := hostcall.Do(hostConfigGet, abi.ConfigGetInput{Key: key}, &out); err != nil || !out.Found {
		return defaultValue
	}
	return out.Value
}

// GetString returns key's configured string value, or defaultValue if
// unset or not a string.
func GetString(key string, defaultValue string) string {
	if v, ok := Get(key, defaultValue).(string); ok {
		return v
	}
	return defaultValue
}

// GetBool returns key's configured boolean value, or defaultValue if
// unset or not a boolean.
func GetBool(key string, defaultValue bool) bool {
	if v, ok := Get(key, defaultValue).(bool); ok {
		return v
	}
	return defaultValue
}

// GetInt returns key's configured integer value, or defaultValue if
// unset or not a number.
func GetInt(key string, defaultValue int) int {
	if n, ok := asInt64(Get(key, nil)); ok {
		return int(n)
	}
	return defaultValue
}

// GetFloat returns key's configured float value, or defaultValue if
// unset or not a number.
func GetFloat(key string, defaultValue float64) float64 {
	if f, ok := asFloat64(Get(key, nil)); ok {
		return f
	}
	return defaultValue
}

// GetDuration returns key's configured duration value, or defaultValue if
// unset. A string value is parsed with time.ParseDuration (e.g. "5m"); a
// number value is treated as a count of nanoseconds.
func GetDuration(key string, defaultValue time.Duration) time.Duration {
	v := Get(key, nil)
	if s, ok := v.(string); ok {
		if d, err := time.ParseDuration(s); err == nil {
			return d
		}
		return defaultValue
	}
	if n, ok := asInt64(v); ok {
		return time.Duration(n)
	}
	return defaultValue
}

// GetStringSlice returns key's configured "string[]" value, or
// defaultValue if unset or not every element is a string.
func GetStringSlice(key string, defaultValue []string) []string {
	items, ok := Get(key, nil).([]any)
	if !ok {
		return defaultValue
	}
	out := make([]string, len(items))
	for i, item := range items {
		s, ok := item.(string)
		if !ok {
			return defaultValue
		}
		out[i] = s
	}
	return out
}

// GetIntSlice returns key's configured "integer[]" value, or
// defaultValue if unset or not every element is a number.
func GetIntSlice(key string, defaultValue []int) []int {
	items, ok := Get(key, nil).([]any)
	if !ok {
		return defaultValue
	}
	out := make([]int, len(items))
	for i, item := range items {
		n, ok := asInt64(item)
		if !ok {
			return defaultValue
		}
		out[i] = int(n)
	}
	return out
}

// GetFloatSlice returns key's configured "float[]" value, or
// defaultValue if unset or not every element is a number.
func GetFloatSlice(key string, defaultValue []float64) []float64 {
	items, ok := Get(key, nil).([]any)
	if !ok {
		return defaultValue
	}
	out := make([]float64, len(items))
	for i, item := range items {
		f, ok := asFloat64(item)
		if !ok {
			return defaultValue
		}
		out[i] = f
	}
	return out
}

// Set writes key's value programmatically. Modules should use this
// sparingly — config is normally set by tenant admins through the
// settings UI (host-abi-reference.md §14). Only a key declared in the
// calling module's own config_schema can be set; the engine rejects
// anything else with config.key_not_declared.
func Set(key string, value any) error {
	return hostcall.Do(hostConfigSet, abi.ConfigSetInput{Key: key, Value: value}, nil)
}

// asInt64/asFloat64 coerce a msgpack-decoded numeric any (int64 or
// float64, depending on how the host encoded it) to the caller's
// requested numeric type.
func asInt64(v any) (int64, bool) {
	switch n := v.(type) {
	case int64:
		return n, true
	case float64:
		return int64(n), true
	default:
		return 0, false
	}
}

func asFloat64(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case int64:
		return float64(n), true
	default:
		return 0, false
	}
}
