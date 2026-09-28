package wasm

import (
	"context"
	"encoding/json/v2"
	"fmt"
	"strconv"
	"strings"

	abiv1 "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/djangbahevans/goerp/internal/engine/abi"
	"github.com/djangbahevans/goerp/internal/engine/manifest"
	"github.com/djangbahevans/goerp/internal/engine/tenantschema"
	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
	"github.com/vmihailenco/msgpack/v5"
)

// registerHostConfig attaches host.config.get/set to the runtime.
// host.config needs no capability (host-abi-reference.md §4 lists it
// "none (always available)"), unlike most other host.* namespaces.
func registerHostConfig(ctx context.Context, rt wazero.Runtime, r *Runtime) error {
	_, err := rt.NewHostModuleBuilder("host.config").
		NewFunctionBuilder().WithFunc(makeConfigGet(r)).Export("get").
		NewFunctionBuilder().WithFunc(makeConfigSet(r)).Export("set").
		Instantiate(ctx)
	return err
}

// ownConfigEntry validates that key is "{caller's own module}.{subKey}"
// and that subKey is declared in the caller's own config_schema —
// host.config.get/set both reject a key belonging to another module or
// undeclared by the caller's own manifest (host-abi-reference.md §14
// "Only keys declared in the module's config_schema can be set" — get
// applies the identical restriction, since decoding a value to its
// declared type requires knowing that declaration).
func ownConfigEntry(modCtx *ModuleContext, key string) (manifest.ConfigEntry, string, *abiv1.HostError) {
	subKey, ok := strings.CutPrefix(key, modCtx.ModuleName+".")
	if !ok || subKey == "" {
		return manifest.ConfigEntry{}, "", configKeyNotDeclared(key)
	}

	entry, ok := modCtx.ConfigEntry(subKey)
	if !ok {
		return manifest.ConfigEntry{}, "", configKeyNotDeclared(key)
	}
	return entry, subKey, nil
}

func configKeyNotDeclared(key string) *abiv1.HostError {
	return &abiv1.HostError{
		Code:    abiv1.ErrCodeConfigKeyNotDeclared,
		Message: fmt.Sprintf("config key %q is not declared in this module's own config_schema", key),
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

		entry, _, hostErr := ownConfigEntry(modCtx, input.Key)
		if hostErr != nil {
			return abi.EncodeHostError(ctx, m, allocate, hostErr)
		}

		if r.configResolver == nil {
			return abi.EncodeHostError(ctx, m, allocate, &abiv1.HostError{
				Code:    abiv1.ErrCodeUnavailable,
				Message: "no tenant config resolver is configured",
			})
		}

		raw, found, err := r.configResolver.Get(ctx, modCtx.TenantID, input.Key)
		if err != nil {
			return abi.EncodeHostError(ctx, m, allocate, &abiv1.HostError{
				Code: abiv1.ErrCodeUnavailable, Message: err.Error(), Retry: true,
			})
		}
		if !found {
			return abi.WriteToModule(ctx, m, allocate, abiv1.ConfigGetOutput{Found: false})
		}

		if entry.Encrypted {
			if r.rowCryptKeys == nil {
				return abi.EncodeHostError(ctx, m, allocate, &abiv1.HostError{
					Code:    abiv1.ErrCodeUnavailable,
					Message: "no row encryption key is configured",
				})
			}
			plaintext, err := r.rowCryptKeys.Decrypt([]byte(raw))
			if err != nil {
				return abi.EncodeHostError(ctx, m, allocate, &abiv1.HostError{
					Code: abiv1.ErrCodeConfigEncryptionError, Message: err.Error(),
				})
			}
			raw = string(plaintext)
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

		entry, subKey, hostErr := ownConfigEntry(modCtx, input.Key)
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
		if err := r.configStore.SetModuleConfig(ctx, modCtx.TenantID, tenantSchema, modCtx.ModuleName, subKey, valueJSON, entry.Type, entry.Encrypted, modCtx.UserID); err != nil {
			return abi.EncodeHostError(ctx, m, allocate, &abiv1.HostError{
				Code: abiv1.ErrCodeUnavailable, Message: err.Error(), Retry: true,
			})
		}
		// This instance's own resolver cache is invalidated synchronously
		// here — see ConfigResolver.Invalidate's own doc comment for why
		// Store.Set's NOTIFY-driven Listener path alone isn't enough.
		if r.configResolver != nil {
			r.configResolver.Invalidate(modCtx.TenantID, input.Key)
		}

		return abi.WriteToModule(ctx, m, allocate, abiv1.ConfigSetOutput{})
	}
}

// encodeConfigValue produces module_config.value's on-disk JSONB bytes
// for entry, either the AES-256-GCM-sealed ciphertext (as a JSON string)
// for an "encrypted": true entry, or value's own JSON encoding otherwise.
func encodeConfigValue(r *Runtime, entry manifest.ConfigEntry, value any) ([]byte, *abiv1.HostError) {
	if entry.Encrypted {
		plaintext, ok := value.(string)
		if !ok {
			return nil, &abiv1.HostError{
				Code:    abiv1.ErrCodeDeserializeError,
				Message: "an \"encrypted\": true config key must be set with a string value",
			}
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

// decodeConfigValue parses raw — resolveConfigQuery's `#>> '{}'` text form
// (tenantconfig/resolver.go), or the plaintext an encrypted entry was just
// decrypted to — into entryType's declared Go representation
// (manifest-spec.md §17's 8 config_schema types).
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
