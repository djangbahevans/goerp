// Package configvalue validates, encodes and decodes module config values
// (manifest-spec.md §17 "config_schema"): the one place the type, bounds and
// at-rest encryption rules live, shared by host.config.set and the tenant
// admin config endpoint so a value is accepted or rejected identically on
// both paths.
package configvalue

import (
	"encoding/json/v2"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/auth/rowcrypt"
	"github.com/djangbahevans/goerp/internal/engine/manifest"
)

// InvalidValueError reports a value that does not satisfy its entry's type or
// bounds. Its message is the underlying validation message.
type InvalidValueError struct{ Err error }

func (e *InvalidValueError) Error() string { return e.Err.Error() }
func (e *InvalidValueError) Unwrap() error { return e.Err }

var (
	// ErrNoEncryptionKey is returned when an encrypted entry is written with no
	// row encryption key configured.
	ErrNoEncryptionKey = errors.New("no row encryption key is configured")
	// ErrEncryption wraps a failure to encrypt a value.
	ErrEncryption = errors.New("encrypt config value")
)

// Encode produces module_config.value's on-disk JSONB bytes: AES-256-GCM
// ciphertext of the value's bare-text form (the form Decode parses) for an
// "encrypted": true entry, or value's own JSON encoding otherwise. A value
// that violates the entry's type or bounds returns *InvalidValueError.
func Encode(entry manifest.ConfigEntry, value any, keys *rowcrypt.RowKeySet) ([]byte, error) {
	if err := Validate(entry, value); err != nil {
		return nil, &InvalidValueError{Err: err}
	}

	if entry.Encrypted {
		plaintext, err := Plaintext(value)
		if err == nil {
			// A value that does not decode back would encrypt fine and fail on get.
			_, err = Decode(entry.Type, plaintext)
		}
		if err != nil {
			return nil, &InvalidValueError{Err: err}
		}
		if keys == nil {
			return nil, ErrNoEncryptionKey
		}
		ciphertext, err := keys.Encrypt([]byte(plaintext))
		if err != nil {
			return nil, fmt.Errorf("%w: %w", ErrEncryption, err)
		}
		data, err := json.Marshal(string(ciphertext))
		if err != nil {
			return nil, &InvalidValueError{Err: err}
		}
		return data, nil
	}

	data, err := json.Marshal(value, json.Deterministic(true))
	if err != nil {
		return nil, &InvalidValueError{Err: err}
	}
	return data, nil
}

// Plaintext renders a validated value in the bare-text form that
// Decode parses back into the entry's type: a string as itself,
// a scalar through strconv, anything else as JSON.
func Plaintext(value any) (string, error) {
	switch v := value.(type) {
	case string:
		return v, nil
	case bool:
		return strconv.FormatBool(v), nil
	case int64:
		return strconv.FormatInt(v, 10), nil
	case float64:
		return strconv.FormatFloat(v, 'g', -1, 64), nil
	default:
		data, err := json.Marshal(v, json.Deterministic(true))
		return string(data), err
	}
}

// Validate rejects a value outside the entry's min/max, or one that does not
// match its type.
func Validate(entry manifest.ConfigEntry, value any) error {
	if err := validateType(entry.Type, value); err != nil {
		return err
	}
	return validateBounds(entry, value)
}

// validateBounds enforces min and max on a numeric or duration value;
// the manifest validator has already guaranteed the bounds' own types.
func validateBounds(entry manifest.ConfigEntry, value any) error {
	var n, lo, hi float64
	var hasLo, hasHi bool

	switch entry.Type {
	case "integer", "float":
		n, _ = asFloat64(value)
		lo, hasLo = entry.Min.(float64)
		hi, hasHi = entry.Max.(float64)
	case "duration":
		str, _ := value.(string)
		d, _ := time.ParseDuration(str)
		n = float64(d)
		lo, hasLo = durationBound(entry.Min)
		hi, hasHi = durationBound(entry.Max)
	default:
		return nil
	}

	switch {
	case hasLo && n < lo:
		return fmt.Errorf("config value %v is below the minimum %v", value, entry.Min)
	case hasHi && n > hi:
		return fmt.Errorf("config value %v is above the maximum %v", value, entry.Max)
	}
	return nil
}

func durationBound(bound any) (float64, bool) {
	str, ok := bound.(string)
	if !ok {
		return 0, false
	}
	d, err := time.ParseDuration(str)
	return float64(d), err == nil
}

// validateType rejects a Set value that wouldn't decode back
// through Decode for entryType, so a type mismatch fails at
// Set time rather than surfacing later as a swallowed Get default.
func validateType(entryType string, value any) error {
	switch entryType {
	case "string":
		if _, ok := value.(string); !ok {
			return fmt.Errorf("config value %v is not a string", value)
		}
	case "integer":
		if _, ok := asInt64(value); !ok {
			return fmt.Errorf("config value %v is not an integer", value)
		}
	case "float":
		if _, ok := asFloat64(value); !ok {
			return fmt.Errorf("config value %v is not a number", value)
		}
	case "boolean":
		if _, ok := value.(bool); !ok {
			return fmt.Errorf("config value %v is not a boolean", value)
		}
	case "duration":
		str, ok := value.(string)
		if !ok {
			return fmt.Errorf("config value %v is not a duration string", value)
		}
		if _, err := time.ParseDuration(str); err != nil {
			return fmt.Errorf("config value %q is not a duration: %w", str, err)
		}
	case "string[]", "integer[]", "float[]":
		if _, ok := value.([]any); !ok {
			return fmt.Errorf("config value %v is not an array", value)
		}
	}
	return nil
}

// asInt64/asFloat64 coerce a msgpack-decoded numeric any.
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

// Decode parses raw (resolveConfigQuery's bare-text form, or a
// decrypted plaintext) into entryType's declared Go representation.
func Decode(entryType, raw string) (any, error) {
	switch entryType {
	case "string":
		return raw, nil
	case "integer":
		v, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("decode integer config value: %w", err)
		}
		return v, nil
	case "float":
		v, err := strconv.ParseFloat(raw, 64)
		if err != nil {
			return nil, fmt.Errorf("decode float config value: %w", err)
		}
		return v, nil
	case "boolean":
		v, err := strconv.ParseBool(raw)
		if err != nil {
			return nil, fmt.Errorf("decode boolean config value: %w", err)
		}
		return v, nil
	case "string[]":
		var v []string
		if err := json.Unmarshal([]byte(raw), &v); err != nil {
			return nil, fmt.Errorf("decode string[] config value: %w", err)
		}
		return v, nil
	case "integer[]":
		var v []int64
		if err := json.Unmarshal([]byte(raw), &v); err != nil {
			return nil, fmt.Errorf("decode integer[] config value: %w", err)
		}
		return v, nil
	case "float[]":
		var v []float64
		if err := json.Unmarshal([]byte(raw), &v); err != nil {
			return nil, fmt.Errorf("decode float[] config value: %w", err)
		}
		return v, nil
	case "json":
		var v any
		if err := json.Unmarshal([]byte(raw), &v); err != nil {
			return nil, fmt.Errorf("decode json config value: %w", err)
		}
		return v, nil
	default:
		return raw, nil
	}
}
