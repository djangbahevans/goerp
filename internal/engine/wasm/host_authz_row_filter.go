package wasm

import (
	"context"
	"fmt"
	"strings"

	abiv1 "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/djangbahevans/goerp/internal/engine/abi"
	"github.com/djangbahevans/goerp/internal/engine/domain"
	"github.com/tetratelabs/wazero/api"
	"github.com/vmihailenco/msgpack/v5"
)

// makeAuthzRowFilter serves host.authz.row_filter: the WHERE fragment the
// RLS policies on a table amount to for the request's own user and a
// permission. It is a read-only introspection primitive; RLS filters every
// query regardless of whether a module appends the fragment.
func makeAuthzRowFilter(r *Runtime) func(ctx context.Context, m api.Module, ptr, length uint32) uint64 {
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
		var input abiv1.AuthzRowFilterInput
		if err := msgpack.Unmarshal(inputBytes, &input); err != nil {
			return abi.EncodeHostError(ctx, m, allocate, abi.DeserializeError(err))
		}

		output, hostErr := rowFilter(modCtx, input)
		if hostErr != nil {
			return abi.EncodeHostError(ctx, m, allocate, hostErr)
		}
		return abi.WriteToModule(ctx, m, allocate, output)
	}
}

// rowFilter builds the fragment for input.TableName and input.Permission.
// A caller who lacks the permission sees no rows ("AND FALSE"); a permission
// no policy on the table scopes adds no restriction (""). Policies combine as
// the RLS policies on the table do: the OR of the permissive ones, ANDed with
// every restrictive one, and "AND FALSE" when only restrictive policies
// exist. One that cannot be compiled for the caller is an error, never an
// unrestricted fragment.
func rowFilter(modCtx *ModuleContext, input abiv1.AuthzRowFilterInput) (abiv1.AuthzRowFilterOutput, *abiv1.HostError) {
	if allowed, _ := evaluatePermissionCheck(modCtx, input.Permission); !allowed {
		return abiv1.AuthzRowFilterOutput{SQL: "AND FALSE"}, nil
	}

	env := domain.Env{
		UserID:    modCtx.UserID,
		ContactID: modCtx.ContactID,
		TenantID:  modCtx.TenantID,
		Roles:     modCtx.Roles,
		HasPermission: func(name string) bool {
			return callerHasPermission(modCtx, modCtx.PermissionRegistry(), name)
		},
	}

	var permissive, restrictive []string
	var params []any
	for _, p := range modCtx.PolicyRegistry().For(input.Permission) {
		// An unresolved policy has no table to compare, so it must fail the
		// filter rather than be skipped as belonging to another table.
		if p.Unresolved != nil {
			return abiv1.AuthzRowFilterOutput{}, policyEvaluationError(p.Name, p.Unresolved)
		}
		if p.Table != input.TableName {
			continue
		}

		sql, args, err := domain.CompileToFilter(p.Expr, env, len(params))
		if err != nil {
			return abiv1.AuthzRowFilterOutput{}, policyEvaluationError(p.Name, err)
		}
		if p.Restrictive {
			restrictive = append(restrictive, sql)
		} else {
			permissive = append(permissive, sql)
		}
		params = append(params, args...)
	}

	switch {
	case len(permissive) == 0 && len(restrictive) == 0:
		return abiv1.AuthzRowFilterOutput{}, nil
	case len(permissive) == 0:
		return abiv1.AuthzRowFilterOutput{SQL: "AND FALSE"}, nil
	}
	admitted := strings.Join(permissive, " OR ")
	if len(restrictive) > 0 {
		admitted = strings.Join(append([]string{"(" + admitted + ")"}, restrictive...), " AND ")
	}
	return abiv1.AuthzRowFilterOutput{SQL: "AND (" + admitted + ")", Params: params}, nil
}

func policyEvaluationError(policyName string, err error) *abiv1.HostError {
	return &abiv1.HostError{
		Code:    abiv1.ErrCodeAuthzPolicyEvaluation,
		Message: fmt.Sprintf("policy %q: %v", policyName, err),
	}
}
