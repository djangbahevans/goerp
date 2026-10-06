package wasm

import (
	"context"
	"encoding/json/v2"
	"fmt"
	"strconv"
	"strings"
	"time"

	abiv1 "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/djangbahevans/goerp/internal/engine/abi"
	"github.com/djangbahevans/goerp/internal/engine/auth/rowcrypt"
	"github.com/djangbahevans/goerp/internal/engine/manifest"
	"github.com/djangbahevans/goerp/internal/engine/tenantschema"
	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
	"github.com/vmihailenco/msgpack/v5"
)

// registerHostConfig attaches host.config.get/set to the runtime.
// host.config needs no capability, unlike most other host.* namespaces.
func registerHostConfig(ctx context.Context, rt wazero.Runtime, r *Runtime) error {
	_, err := r.guardedHostModule(rt, "host.config").
		NewFunctionBuilder().WithFunc(makeConfigGet(r)).Export("get").
		NewFunctionBuilder().WithFunc(makeConfigSet(r)).Export("set").
		Instantiate(ctx)
	return err
}

// ownConfigEntry resolves key, a short key with no module prefix, to the
// caller's own config_schema entry and its qualified "{module}.{key}" name.
func ownConfigEntry(modCtx *ModuleContext, key string) (manifest.ConfigEntry, string, *abiv1.HostError) {
	entry, ok := modCtx.ConfigEntry(key)
	if !ok {
		return manifest.ConfigEntry{}, "", configKeyUndeclared(key)
	}
	return entry, modCtx.ModuleName + "." + key, nil
}

// readableConfigEntry resolves key for host.config.get: a short key in the
// caller's own config_schema, or a "{module}.{key}" name in its uses_config.
// loaded is false for a uses_config key whose owner is a soft dependency that
// is not loaded, which has no value to read.
func readableConfigEntry(modCtx *ModuleContext, key string) (entry manifest.ConfigEntry, qualified string, loaded bool, hostErr *abiv1.HostError) {
	if !strings.Contains(key, ".") {
		entry, qualified, hostErr = ownConfigEntry(modCtx, key)
		return entry, qualified, hostErr == nil, hostErr
	}

	ref, ok := modCtx.UsesConfigEntry(key)
	if !ok {
		return manifest.ConfigEntry{}, "", false, configKeyUndeclared(key)
	}
	return ref.Entry, key, ref.Loaded, nil
}

func configKeyUndeclared(key string) *abiv1.HostError {
	return &abiv1.HostError{
		Code:    abiv1.ErrCodeConfigKeyUndeclared,
		Message: fmt.Sprintf("config key %q is neither in this module's own config_schema nor its uses_config", key),
	}
}

func makeConfigGet(r *Runtime) func(ctx context.Context, m api.Module, ptr, length uint32) uint64 {
	return func(ctx context.Context, m api.Module, ptr, length uint32) uint64 {
		inst := r.InstanceForModule(m)
		modCtx := inst.ModuleContext()
		allocate := inst.allocate

		inputBytes, err := abi.ReadFromModule(m.Memory(), ptr, length)
		if err != nil {
			return abi.EncodeHostError(ctx, m, allocate, abi.MemoryFault())
		}
		var input abiv1.ConfigGetInput
		if err := msgpack.Unmarshal(inputBytes, &input); err != nil {
			return abi.EncodeHostError(ctx, m, allocate, abi.DeserializeError(err))
		}

		entry, qualifiedKey, loaded, hostErr := readableConfigEntry(modCtx, input.Key)
		if hostErr != nil {
			return abi.EncodeHostError(ctx, m, allocate, hostErr)
		}
		if !loaded {
			return abi.WriteToModule(ctx, m, allocate, abiv1.ConfigGetOutput{Found: false})
		}

		if r.configResolver == nil {
			return abi.EncodeHostError(ctx, m, allocate, &abiv1.HostError{
				Code:    abiv1.ErrCodeUnavailable,
				Message: "no tenant config resolver is configured",
			})
		}

		raw, encrypted, found, err := r.configResolver.Get(ctx, modCtx.TenantID, qualifiedKey)
		if err != nil {
			return abi.EncodeHostError(ctx, m, allocate, &abiv1.HostError{
				Code: abiv1.ErrCodeUnavailable, Message: err.Error(), Retry: true,
			})
		}
		if !found {
			return abi.WriteToModule(ctx, m, allocate, abiv1.ConfigGetOutput{Found: false})
		}

		if encrypted {
			if r.rowCryptKeys == nil {
				return abi.EncodeHostError(ctx, m, allocate, &abiv1.HostError{
					Code:    abiv1.ErrCodeUnavailable,
					Message: "no row encryption key is configured",
				})
			}
			decrypted, hostErr := decryptConfigValue(r.rowCryptKeys, raw)
			if hostErr != nil {
				return abi.EncodeHostError(ctx, m, allocate, hostErr)
			}
			raw = decrypted
		}

		value, err := decodeConfigValue(entry.Type, raw)
		if err != nil {
			return abi.EncodeHostError(ctx, m, allocate, abi.DeserializeError(err))
		}

		return abi.WriteToModule(ctx, m, allocate, abiv1.ConfigGetOutput{Value: value, Found: true})
	}
}

func makeConfigSet(r *Runtime) func(ctx context.Context, m api.Module, ptr, length uint32) uint64 {
	return func(ctx context.Context, m api.Module, ptr, length uint32) uint64 {
		inst := r.InstanceForModule(m)
		modCtx := inst.ModuleContext()
		allocate := inst.allocate

		inputBytes, err := abi.ReadFromModule(m.Memory(), ptr, length)
		if err != nil {
			return abi.EncodeHostError(ctx, m, allocate, abi.MemoryFault())
		}
		var input abiv1.ConfigSetInput
		if err := msgpack.Unmarshal(inputBytes, &input); err != nil {
			return abi.EncodeHostError(ctx, m, allocate, abi.DeserializeError(err))
		}

		entry, qualifiedKey, hostErr := ownConfigEntry(modCtx, input.Key)
		if hostErr != nil {
			return abi.EncodeHostError(ctx, m, allocate, hostErr)
		}

		if r.configStore == nil {
			return abi.EncodeHostError(ctx, m, allocate, &abiv1.HostError{
				Code:    abiv1.ErrCodeUnavailable,
				Message: "no tenant config store is configured",
			})
		}

		valueJSON, hostErr := encodeConfigValue(r, entry, input.Value)
		if hostErr != nil {
			return abi.EncodeHostError(ctx, m, allocate, hostErr)
		}

		tenantSchema := tenantschema.Name(modCtx.TenantSlug)
		if err := r.configStore.SetModuleConfig(ctx, modCtx.TenantID, tenantSchema, modCtx.ModuleName, input.Key, valueJSON, entry.Type, entry.Encrypted, modCtx.UserID); err != nil {
			return abi.EncodeHostError(ctx, m, allocate, &abiv1.HostError{
				Code: abiv1.ErrCodeUnavailable, Message: err.Error(), Retry: true,
			})
		}
		// Invalidate this instance's own cache synchronously; see
		// ConfigResolver.Invalidate's doc comment.
		if r.configResolver != nil {
			r.configResolver.Invalidate(modCtx.TenantID, qualifiedKey)
		}

		return abi.WriteToModule(ctx, m, allocate, abiv1.ConfigSetOutput{})
	}
}

// decryptConfigValue opens raw, a value the resolver reported as
// ciphertext; any failure, including a key id no longer in keys, errors
// rather than returning ciphertext as a secret.
func decryptConfigValue(keys *rowcrypt.RowKeySet, raw string) (string, *abiv1.HostError) {
	plaintext, err := keys.Decrypt([]byte(raw))
	if err != nil {
		return "", &abiv1.HostError{Code: abiv1.ErrCodeConfigEncryptionError, Message: err.Error()}
	}
	return string(plaintext), nil
}

// encodeConfigValue produces module_config.value's on-disk JSONB bytes:
// AES-256-GCM ciphertext of the value's bare-text form (the form
// decodeConfigValue parses) for an "encrypted": true entry, or value's own
// JSON encoding otherwise.
func encodeConfigValue(r *Runtime, entry manifest.ConfigEntry, value any) ([]byte, *abiv1.HostError) {
	if err := validateConfigValue(entry, value); err != nil {
		return nil, &abiv1.HostError{Code: abiv1.ErrCodeDeserializeError, Message: err.Error()}
	}

	if entry.Encrypted {
		plaintext, err := configPlaintext(value)
		if err == nil {
			// A value that does not decode back would encrypt fine and fail on get.
			_, err = decodeConfigValue(entry.Type, plaintext)
		}
		if err != nil {
			return nil, &abiv1.HostError{Code: abiv1.ErrCodeDeserializeError, Message: err.Error()}
		}
		if r.rowCryptKeys == nil {
			return nil, &abiv1.HostError{
				Code:    abiv1.ErrCodeUnavailable,
				Message: "no row encryption key is configured",
			}
		}
		ciphertext, err := r.rowCryptKeys.Encrypt([]byte(plaintext))
		if err != nil {
			return nil, &abiv1.HostError{Code: abiv1.ErrCodeConfigEncryptionError, Message: err.Error()}
		}
		data, err := json.Marshal(string(ciphertext))
		if err != nil {
			return nil, &abiv1.HostError{Code: abiv1.ErrCodeDeserializeError, Message: err.Error()}
		}
		return data, nil
	}

	data, err := json.Marshal(value, json.Deterministic(true))
	if err != nil {
		return nil, &abiv1.HostError{Code: abiv1.ErrCodeDeserializeError, Message: err.Error()}
	}
	return data, nil
}

// configPlaintext renders a validated value in the bare-text form that
// decodeConfigValue parses back into the entry's type: a string as itself,
// a scalar through strconv, anything else as JSON.
func configPlaintext(value any) (string, error) {
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

// validateConfigValue rejects a Set value outside the entry's min/max, or one
// that validateConfigValueType rejects.
func validateConfigValue(entry manifest.ConfigEntry, value any) error {
	if err := validateConfigValueType(entry.Type, value); err != nil {
		return err
	}
	return validateConfigBounds(entry, value)
}

// validateConfigBounds enforces min and max on a numeric or duration value;
// the manifest validator has already guaranteed the bounds' own types.
func validateConfigBounds(entry manifest.ConfigEntry, value any) error {
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

// validateConfigValueType rejects a Set value that wouldn't decode back
// through decodeConfigValue for entryType, so a type mismatch fails at
// Set time rather than surfacing later as a swallowed Get default.
func validateConfigValueType(entryType string, value any) error {
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

// decodeConfigValue parses raw (resolveConfigQuery's bare-text form, or a
// decrypted plaintext) into entryType's declared Go representation.
func decodeConfigValue(entryType, raw string) (any, error) {
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
