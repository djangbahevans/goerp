package wasm

import (
	"context"

	"github.com/djangbahevans/goerp/internal/engine/abi"
	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
	"github.com/vmihailenco/msgpack/v5"
)

// registerHostAuthz attaches host.authz.field_check to the runtime.
func registerHostAuthz(ctx context.Context, rt wazero.Runtime, r *Runtime) error {
	_, err := rt.NewHostModuleBuilder("host.authz").
		NewFunctionBuilder().WithFunc(makeAuthzFieldCheck(r)).Export("field_check").
		Instantiate(ctx)
	return err
}

// authzFieldCheckKind mirrors sdk/go/authz.AccessKind's wire values.
type authzFieldCheckKind int

const (
	authzFieldCheckRead authzFieldCheckKind = iota
	authzFieldCheckWrite
)

type authzFieldCheckInput struct {
	UserID string              `msgpack:"user_id"`
	Model  string              `msgpack:"model"`
	Field  string              `msgpack:"field"`
	Kind   authzFieldCheckKind `msgpack:"kind"`
}

type authzFieldCheckOutput struct {
	Allowed bool `msgpack:"allowed"`
}

// makeAuthzFieldCheck reports whether modCtx's caller may read or write
// modelName.fieldName, per the field's declared FieldSecurityRule (if
// any) — a no-rule field is always allowed. userID is the caller's own
// user (host_orm.go's field-security enforcement uses the same
// modCtx.PermissionSet as the sole source of truth; there is no
// mechanism to resolve an arbitrary third party's permission set from
// inside a WASM host function), so it is validated against modCtx.UserID
// rather than used to look up a different caller's permissions.
func makeAuthzFieldCheck(r *Runtime) func(ctx context.Context, m api.Module, ptr, length uint32) uint64 {
	return func(ctx context.Context, m api.Module, ptr, length uint32) uint64 {
		inst := r.InstanceForModule(m)
		modCtx := inst.ModuleContext()
		allocate := inst.allocate

		if !modCtx.Capabilities().Has(abi.CapAuthzCheck) {
			return abi.EncodeHostError(ctx, m, allocate, abi.CapabilityDenied("authz.check"))
		}

		inputBytes, err := abi.ReadFromModule(m.Memory(), ptr, length)
		if err != nil {
			return abi.EncodeHostError(ctx, m, allocate, abi.MemoryFault())
		}
		var input authzFieldCheckInput
		if err := msgpack.Unmarshal(inputBytes, &input); err != nil {
			return abi.EncodeHostError(ctx, m, allocate, abi.DeserializeError(err))
		}

		if input.UserID != modCtx.UserID {
			return abi.EncodeHostError(ctx, m, allocate, &abi.HostError{
				Code:    "authz.user_id_mismatch",
				Message: "user_id must match the caller's own user",
			})
		}

		allowed := evaluateFieldCheck(modCtx, input.Model, input.Field, input.Kind)
		return abi.WriteToModule(ctx, m, allocate, authzFieldCheckOutput{Allowed: allowed})
	}
}

// evaluateFieldCheck reports whether modCtx's caller may access
// modelName.fieldName per the field's declared FieldSecurityRule — a
// field with no declared rule, or a rule with no permission set for
// kind, is always allowed.
func evaluateFieldCheck(modCtx *ModuleContext, modelName, fieldName string, kind authzFieldCheckKind) bool {
	fieldSecReg := modCtx.FieldSecRegistry()
	if fieldSecReg == nil {
		return true
	}
	rule, ok := fieldSecReg.Rule(modelName, fieldName)
	if !ok {
		return true
	}

	permissionName := rule.ReadPermission
	if kind == authzFieldCheckWrite {
		permissionName = rule.WritePermission
	}
	if permissionName == "" {
		return true
	}

	return callerHasPermission(modCtx, modCtx.PermissionRegistry(), permissionName)
}
