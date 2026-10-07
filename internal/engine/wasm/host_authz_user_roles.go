package wasm

import (
	"context"
	"database/sql"
	"fmt"

	abiv1 "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/djangbahevans/goerp/internal/engine/abi"
	"github.com/djangbahevans/goerp/internal/engine/tenantschema"
	"github.com/tetratelabs/wazero/api"
)

// makeAuthzUserRoles serves host.authz.user_roles: the roles the request's
// own user currently holds in the tenant. It reads the role grants rather than
// the role names the request carries, because the answer includes each role's
// id and whether it is built in.
func makeAuthzUserRoles(r *Runtime) func(ctx context.Context, m api.Module, ptr, length uint32) uint64 {
	return func(ctx context.Context, m api.Module, ptr, length uint32) uint64 {
		inst := r.InstanceForModule(m)
		modCtx := inst.ModuleContext()
		allocate := inst.allocate

		if !modCtx.Capabilities().Has(abi.CapAuthzCheck) {
			return abi.EncodeHostError(ctx, m, allocate, abi.CapabilityDenied("authz.check"))
		}

		db := r.schemaSyncDB.Load()
		if db == nil {
			return abi.EncodeHostError(ctx, m, allocate, &abiv1.HostError{Code: abiv1.ErrCodeUnavailable, Message: "no schema-sync database is configured"})
		}

		roles, err := userRoles(ctx, db, modCtx)
		if err != nil {
			return abi.EncodeHostError(ctx, m, allocate, &abiv1.HostError{Code: abiv1.ErrCodeUnavailable, Message: err.Error(), Retry: true})
		}
		return abi.WriteToModule(ctx, m, allocate, abiv1.AuthzUserRolesOutput{Roles: roles})
	}
}

// userRoles lists modCtx's user's unexpired role grants, ordered by name. A
// context with no user, such as a background activity, holds none.
func userRoles(ctx context.Context, db *sql.DB, modCtx *ModuleContext) ([]abiv1.AuthzRole, error) {
	roles := []abiv1.AuthzRole{}
	if modCtx.UserID == "" {
		return roles, nil
	}

	schema := tenantschema.Name(modCtx.TenantSlug)
	query := fmt.Sprintf(`
		SELECT r.id::text, r.name, r.is_immutable
		FROM %[1]s.user_roles ur
		JOIN %[1]s.roles r ON r.id = ur.role_id
		WHERE ur.user_id = $1 AND (ur.expires_at IS NULL OR ur.expires_at > NOW())
		ORDER BY r.name`, schema)
	rows, err := db.QueryContext(ctx, query, modCtx.UserID)
	if err != nil {
		return nil, fmt.Errorf("list user roles: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var role abiv1.AuthzRole
		if err := rows.Scan(&role.ID, &role.Name, &role.IsSystem); err != nil {
			return nil, fmt.Errorf("list user roles: %w", err)
		}
		roles = append(roles, role)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list user roles: %w", err)
	}
	return roles, nil
}
