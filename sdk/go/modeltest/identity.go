package modeltest

import (
	"context"
	"database/sql"

	"github.com/djangbahevans/goerp/internal/engine/role"
	"github.com/djangbahevans/goerp/internal/engine/tenant"
	"github.com/djangbahevans/goerp/internal/engine/tenantconfig"
	"github.com/djangbahevans/goerp/internal/engine/user"
)

func (h *Harness) setupIdentity(db *sql.DB, cfg *harnessConfig) {
	h.t.Helper()
	h.tenants = tenant.NewStore(db)
	h.roles = role.NewStore(db)
	users := user.NewStore(db)

	for _, bootstrap := range []func(context.Context) error{
		h.tenants.Bootstrap,
		users.Bootstrap,
		tenantconfig.NewStore(db).Bootstrap,
	} {
		if err := bootstrap(h.t.Context()); err != nil {
			h.t.Fatalf("modeltest: bootstrap tenant identity: %v", err)
		}
	}

	// Provisioning status excludes test tenants from shared active-tenant workers.
	if _, err := h.tenants.ReserveSlug(h.t.Context(), h.TenantID, h.tenantSlug, "Module test tenant"); err != nil {
		h.t.Fatalf("modeltest: register tenant: %v", err)
	}
	h.t.Cleanup(func() {
		if _, err := db.ExecContext(context.Background(), `DELETE FROM system.river_job WHERE args->>'tenant_id' = $1`, h.TenantID); err != nil {
			h.t.Errorf("modeltest: delete tenant jobs: %v", err)
		}
		if _, err := db.ExecContext(context.Background(), `DELETE FROM system.tenants WHERE id = $1`, h.TenantID); err != nil {
			h.t.Errorf("modeltest: delete tenant: %v", err)
		}
	})

	result, err := db.ExecContext(h.t.Context(), `INSERT INTO system.users (id, email)
		VALUES ($1, $2) ON CONFLICT (id) DO NOTHING`, h.UserID, h.UserID+"@modeltest.invalid")
	if err != nil {
		h.t.Fatalf("modeltest: create user: %v", err)
	}
	inserted, err := result.RowsAffected()
	if err != nil {
		h.t.Fatalf("modeltest: check user creation: %v", err)
	}
	if inserted != 0 {
		h.t.Cleanup(func() {
			if _, err := db.ExecContext(context.Background(), `DELETE FROM system.users WHERE id = $1`, h.UserID); err != nil {
				h.t.Errorf("modeltest: delete user: %v", err)
			}
		})
	}

	if _, err := db.ExecContext(h.t.Context(), "INSERT INTO "+h.DB.schema()+".tenant_members (user_id, contact_id) VALUES ($1, $1)", h.UserID); err != nil {
		h.t.Fatalf("modeltest: create tenant membership: %v", err)
	}
	if err := h.roles.SeedBuiltinRoles(h.t.Context(), h.tenantSlug); err != nil {
		h.t.Fatalf("modeltest: seed roles: %v", err)
	}
	roleName := "admin"
	if cfg.userID != "" {
		roleName = "user"
	}
	roleID, err := h.roles.GetRoleByName(h.t.Context(), h.tenantSlug, roleName)
	if err != nil {
		h.t.Fatalf("modeltest: find default role: %v", err)
	}
	if err := h.roles.AssignRole(h.t.Context(), h.tenantSlug, h.UserID, roleID, ""); err != nil {
		h.t.Fatalf("modeltest: assign default role: %v", err)
	}
}
