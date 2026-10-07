package role

import (
	"context"
	"fmt"
	"slices"
	"testing"

	"github.com/djangbahevans/goerp/internal/engine/manifest"
	"github.com/djangbahevans/goerp/internal/engine/tenantschema"
)

func grantedTo(t *testing.T, store *Store, slug, roleName string) []string {
	t.Helper()
	roleID, err := store.GetRoleByName(context.Background(), slug, roleName)
	if err != nil {
		t.Fatalf("GetRoleByName(%q) error: %v", roleName, err)
	}
	rows, err := store.db.QueryContext(context.Background(),
		fmt.Sprintf("SELECT permission_name FROM %s.role_permissions WHERE role_id = $1 ORDER BY permission_name", tenantschema.Name(slug)), roleID)
	if err != nil {
		t.Fatalf("query grants: %v", err)
	}
	defer func() { _ = rows.Close() }()
	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		names = append(names, name)
	}
	return names
}

func TestGrantModuleDefaults_GrantsDeclaredRolesAndIsIdempotent(t *testing.T) {
	store, _, slug := openTestStore(t)
	ctx := context.Background()
	if err := store.SeedBuiltinRoles(ctx, slug); err != nil {
		t.Fatal(err)
	}
	perms := []manifest.Permission{
		{Name: "contacts:contact:read", DefaultRoles: []string{"user", "admin"}},
		{Name: "contacts:contact:delete", DefaultRoles: []string{"admin"}},
		{Name: "contacts:contact:export"},
	}

	for range 2 {
		if err := store.GrantModuleDefaults(ctx, slug, perms); err != nil {
			t.Fatalf("GrantModuleDefaults() error: %v", err)
		}
	}

	if got, want := grantedTo(t, store, slug, "admin"), []string{"contacts:contact:delete", "contacts:contact:read"}; !slices.Equal(got, want) {
		t.Errorf("admin grants = %v, want %v", got, want)
	}
	if got, want := grantedTo(t, store, slug, "user"), []string{"contacts:contact:read"}; !slices.Equal(got, want) {
		t.Errorf("user grants = %v, want %v", got, want)
	}
	if got := grantedTo(t, store, slug, "portal"); len(got) != 0 {
		t.Errorf("portal grants = %v, want none", got)
	}
}

func TestGrantModuleDefaults_SkipsUnknownRoleAndMissingTables(t *testing.T) {
	store, _, slug := openTestStore(t)
	ctx := context.Background()
	if err := store.SeedBuiltinRoles(ctx, slug); err != nil {
		t.Fatal(err)
	}

	err := store.GrantModuleDefaults(ctx, slug, []manifest.Permission{
		{Name: "contacts:contact:read", DefaultRoles: []string{"typo", "admin"}},
	})
	if err != nil {
		t.Fatalf("unknown default role: %v", err)
	}
	if got := grantedTo(t, store, slug, "admin"); !slices.Equal(got, []string{"contacts:contact:read"}) {
		t.Errorf("admin grants = %v, want the known role still granted", got)
	}

	err = store.GrantModuleDefaults(ctx, "roletestnoschema", []manifest.Permission{
		{Name: "contacts:contact:read", DefaultRoles: []string{"admin"}},
	})
	if err != nil {
		t.Errorf("tenant without RBAC tables: %v", err)
	}
}
