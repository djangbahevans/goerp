package wasm

import (
	"testing"
	"uuid"

	"github.com/djangbahevans/goerp/internal/engine/manifest"
	"github.com/djangbahevans/goerp/internal/engine/permission"
)

func TestTenantScopePermissionContext(t *testing.T) {
	primary := openTestPrimaryDB(t)
	registry := permission.NewPermissionRegistry()
	registry.Register("contacts", []manifest.Permission{
		{Name: "contacts:identifier:read"},
		{Name: "contacts:personal:read"},
	})
	slug := "scopepermissions" + uuid.New().String()[:8]
	createFixtureTenantSchema(t, primary, slug)
	mc := newORMTestModuleContext(slug, nil)
	mc.snapshot.PermissionRegistry = registry
	readIndex, _ := registry.Index("contacts:identifier:read")
	personalIndex, _ := registry.Index("contacts:personal:read")

	var readBits, personalBits permission.PermissionBitfield
	readBits.Set(readIndex)
	personalBits.Set(personalIndex)

	for _, tc := range []struct {
		name        string
		permissions permission.PermissionBitfield
		want        string
	}{
		{"none", nil, ""},
		{"read", readBits, "contacts:identifier:read"},
		{"personal", personalBits, "contacts:personal:read"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mc.PermissionSet = tc.permissions
			tx, err := primary.BeginTx(t.Context(), nil)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = tx.Rollback() }()

			if err := applyTenantScopeAsTenantRole(t.Context(), tx, mc); err != nil {
				t.Fatal(err)
			}
			var got string
			if err := tx.QueryRowContext(t.Context(), `SELECT current_setting('app.current_user_permissions')`).Scan(&got); err != nil {
				t.Fatal(err)
			}
			if got != tc.want {
				t.Fatalf("permissions=%q, want %q", got, tc.want)
			}
		})
	}

	if got := tenantPermissionNames(newORMTestModuleContext("scope_permissions", nil)); got != "" {
		t.Fatalf("missing registry granted permissions: %q", got)
	}
}
