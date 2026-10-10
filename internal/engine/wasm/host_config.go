package wasm

import (
	"context"
	"errors"
	"fmt"
	"strings"

	abiv1 "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/djangbahevans/goerp/internal/engine/abi"
	"github.com/djangbahevans/goerp/internal/engine/auth/rowcrypt"
	"github.com/djangbahevans/goerp/internal/engine/configvalue"
	"github.com/djangbahevans/goerp/internal/engine/manifest"
	"github.com/djangbahevans/goerp/internal/engine/tenantschema"
	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
	"github.com/vmihailenco/msgpack/v5"
)

// host.config needs no capability, unlike most other host.* namespaces.
func registerHostConfig(ctx context.Context, rt wazero.Runtime, r *Runtime) error {
	_, err := r.guardedHostModule(rt, "host.config").
		NewFunctionBuilder().WithFunc(makeConfigGet(r)).Export("get").
		NewFunctionBuilder().WithFunc(makeConfigSet(r)).Export("set").
		Instantiate(ctx)
	return err
}

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

		if strings.HasPrefix(input.Key, "company.") {
			output, hostErr := r.readCompanyConfig(ctx, modCtx.TenantID, input.Key)
			if hostErr != nil {
				return abi.EncodeHostError(ctx, m, allocate, hostErr)
			}

			return abi.WriteToModule(ctx, m, allocate, output)
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

		value, err := configvalue.Decode(entry.Type, raw)
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

// encodeConfigValue produces module_config.value's on-disk JSONB bytes through
// configvalue.Encode, mapping its errors onto host error codes.
func encodeConfigValue(r *Runtime, entry manifest.ConfigEntry, value any) ([]byte, *abiv1.HostError) {
	data, err := configvalue.Encode(entry, value, r.rowCryptKeys)
	if err == nil {
		return data, nil
	}
	if _, invalid := errors.AsType[*configvalue.InvalidValueError](err); invalid {
		return nil, &abiv1.HostError{Code: abiv1.ErrCodeDeserializeError, Message: err.Error()}
	}
	if errors.Is(err, configvalue.ErrNoEncryptionKey) {
		return nil, &abiv1.HostError{Code: abiv1.ErrCodeUnavailable, Message: err.Error()}
	}
	return nil, &abiv1.HostError{Code: abiv1.ErrCodeConfigEncryptionError, Message: err.Error()}
}
