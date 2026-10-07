package wasm

import (
	"context"
	"fmt"

	abiv1 "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/djangbahevans/goerp/internal/engine/abi"
	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
	"github.com/vmihailenco/msgpack/v5"
)

// registerHostAuthz attaches host.authz.check, require and field_check to
// the runtime.
func registerHostAuthz(ctx context.Context, rt wazero.Runtime, r *Runtime) error {
	_, err := r.guardedHostModule(rt, "host.authz").
		NewFunctionBuilder().WithFunc(makeAuthzCheck(r)).Export("check").
		NewFunctionBuilder().WithFunc(makeAuthzRequire(r)).Export("require").
		NewFunctionBuilder().WithFunc(makeAuthzFieldCheck(r)).Export("field_check").
		Instantiate(ctx)
	return err
}

// makeAuthzCheck reports whether modCtx's caller holds a permission.
func makeAuthzCheck(r *Runtime) func(ctx context.Context, m api.Module, ptr, length uint32) uint64 {
	return func(ctx context.Context, m api.Module, ptr, length uint32) uint64 {
		return authzCheck(ctx, r, m, ptr, length, false)
	}
}

// makeAuthzRequire is makeAuthzCheck that fails with authz.forbidden
// instead of reporting a denial.
func makeAuthzRequire(r *Runtime) func(ctx context.Context, m api.Module, ptr, length uint32) uint64 {
	return func(ctx context.Context, m api.Module, ptr, length uint32) uint64 {
		return authzCheck(ctx, r, m, ptr, length, true)
	}
}

// authzCheck serves host.authz.check and host.authz.require for the
// request's own user, from modCtx.PermissionSet, which the auth middleware
// hydrated from the 60-second role cache, so there is no lookup to make or
// cache here.
func authzCheck(ctx context.Context, r *Runtime, m api.Module, ptr, length uint32, require bool) uint64 {
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
	var input abiv1.AuthzCheckInput
	if err := msgpack.Unmarshal(inputBytes, &input); err != nil {
		return abi.EncodeHostError(ctx, m, allocate, abi.DeserializeError(err))
	}

	allowed, reason := evaluatePermissionCheck(modCtx, input.Permission)
	if allowed && input.ResourceID != "" {
		if policies := modCtx.PolicyRegistry().For(input.Permission); len(policies) > 0 {
			db := r.schemaSyncDB.Load()
			if db == nil {
				return abi.EncodeHostError(ctx, m, allocate, &abiv1.HostError{Code: abiv1.ErrCodeUnavailable, Message: "no schema-sync database is configured"})
			}
			var hostErr *abiv1.HostError
			allowed, reason, hostErr = evaluateRecordPolicies(ctx, db, modCtx, policies, input.ResourceID)
			if hostErr != nil {
				return abi.EncodeHostError(ctx, m, allocate, hostErr)
			}
		}
	}

	switch {
	case allowed && require:
		return abi.WriteToModule(ctx, m, allocate, struct{}{})
	case allowed:
		return abi.WriteToModule(ctx, m, allocate, abiv1.AuthzCheckOutput{Allowed: true})
	case require:
		return abi.EncodeHostError(ctx, m, allocate, &abiv1.HostError{
			Code:    abiv1.ErrCodeAuthzForbidden,
			Message: reason,
			Details: map[string]any{"permission": input.Permission},
		})
	default:
		return abi.WriteToModule(ctx, m, allocate, abiv1.AuthzCheckOutput{Reason: reason})
	}
}

// evaluatePermissionCheck reports whether modCtx's caller holds
// permissionName, with the reason when it does not. A name no loaded module
// declares is denied like one the caller lacks, since no role can hold it.
func evaluatePermissionCheck(modCtx *ModuleContext, permissionName string) (allowed bool, reason string) {
	permReg := modCtx.PermissionRegistry()
	if permReg == nil {
		return false, fmt.Sprintf("permission %q is not declared by any loaded module", permissionName)
	}
	if _, declared := permReg.Index(permissionName); !declared {
		return false, fmt.Sprintf("permission %q is not declared by any loaded module", permissionName)
	}
	if !callerHasPermission(modCtx, permReg, permissionName) {
		return false, fmt.Sprintf("caller does not hold permission %q", permissionName)
	}
	return true, ""
}

// makeAuthzFieldCheck reports whether modCtx's caller may read or write
// modelName.fieldName, per the field's declared FieldSecurityRule (if
// any) — a no-rule field is always allowed. The answer is for the
// request's own user: host_orm.go's field-security enforcement uses the
// same modCtx.PermissionSet as the sole source of truth.
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
		var input abiv1.AuthzFieldCheckInput
		if err := msgpack.Unmarshal(inputBytes, &input); err != nil {
			return abi.EncodeHostError(ctx, m, allocate, abi.DeserializeError(err))
		}

		allowed := evaluateFieldCheck(modCtx, input.Model, input.Field, input.Kind)
		return abi.WriteToModule(ctx, m, allocate, abiv1.AuthzFieldCheckOutput{Allowed: allowed})
	}
}

// evaluateFieldCheck reports whether modCtx's caller may access
// modelName.fieldName per the field's declared FieldSecurityRule — a
// field with no declared rule, or a rule with no permission set for
// kind, is always allowed.
func evaluateFieldCheck(modCtx *ModuleContext, modelName, fieldName string, kind abiv1.AuthzFieldCheckKind) bool {
	fieldSecReg := modCtx.FieldSecRegistry()
	if fieldSecReg == nil {
		return true
	}
	rule, ok := fieldSecReg.Rule(modelName, fieldName)
	if !ok {
		return true
	}

	permissionName := rule.ReadPermission
	if kind == abiv1.AuthzFieldCheckWrite {
		permissionName = rule.WritePermission
	}
	if permissionName == "" {
		return true
	}

	return callerHasPermission(modCtx, modCtx.PermissionRegistry(), permissionName)
}
