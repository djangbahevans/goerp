package role

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/db"
	"github.com/djangbahevans/goerp/internal/engine/tenantschema"
)

// localPostgresDSN points directly at the compose.dev.yml Postgres
// instance, same convention as internal/engine/tenant's and
// internal/engine/user's tests.
const localPostgresDSN = "postgres://goerp:dev@localhost:55432/goerp"

// openTestStore creates a fixture tenant_<random> schema directly (this
// package's tests don't wait on real tenant provisioning to exist — same
// "hand-built fixtures ahead of the real thing" reasoning goerp#13's notes
// already established) and returns a Store plus that schema's slug for
// tests to target.
func openTestStore(t *testing.T) (store *Store, conn *sql.DB, tenantSlug string) {
	t.Helper()

	conn, err := db.New(localPostgresDSN)
	if err != nil {
		t.Skipf("postgres not reachable at %s (start compose.dev.yml): %v", localPostgresDSN, err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	slug := fmt.Sprintf("roletest%d", time.Now().UnixNano())
	schema := tenantschema.Name(slug)

	if _, err := conn.ExecContext(context.Background(), "CREATE SCHEMA "+schema); err != nil {
		t.Fatalf("create fixture schema: %v", err)
	}
	t.Cleanup(func() {
		_, _ = conn.ExecContext(context.Background(), "DROP SCHEMA "+schema+" CASCADE")
	})

	store = NewStore(conn)
	if err := store.Bootstrap(context.Background(), slug); err != nil {
		t.Fatalf("Bootstrap() error: %v", err)
	}

	return store, conn, slug
}

func TestBootstrap_IsIdempotent(t *testing.T) {
	store, _, slug := openTestStore(t)

	if err := store.Bootstrap(context.Background(), slug); err != nil {
		t.Fatalf("second Bootstrap() call error: %v", err)
	}
}

// TestBootstrap_ConcurrentCallsAgainstFreshSchemaAllSucceed guards
// against goerp#171 directly against the original failure mode — N
// concurrent first-time Bootstrap calls racing on CREATE TABLE IF NOT
// EXISTS against tables that don't exist yet, unlike
// TestBootstrap_IsIdempotent above which only re-runs Bootstrap after
// openTestStore's own call already created everything. This uses its own
// fresh tenant_<random> schema (not openTestStore's) specifically so this
// is the case being tested, and per-test unique schemas make this safe to
// run alongside every other test/package touching Postgres concurrently
// — see tenant/store_test.go's openTestStore doc comment for why a
// shared table couldn't do the same.
func TestBootstrap_ConcurrentCallsAgainstFreshSchemaAllSucceed(t *testing.T) {
	conn, err := db.New(localPostgresDSN)
	if err != nil {
		t.Skipf("postgres not reachable at %s (start compose.dev.yml): %v", localPostgresDSN, err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	slug := fmt.Sprintf("roleconcurrent%d", time.Now().UnixNano())
	schema := tenantschema.Name(slug)
	if _, err := conn.ExecContext(context.Background(), "CREATE SCHEMA "+schema); err != nil {
		t.Fatalf("create fixture schema: %v", err)
	}
	t.Cleanup(func() {
		_, _ = conn.ExecContext(context.Background(), "DROP SCHEMA "+schema+" CASCADE")
	})

	store := NewStore(conn)

	var wg sync.WaitGroup
	errs := make(chan error, 5)
	for range 5 {
		wg.Go(func() {
			errs <- store.Bootstrap(context.Background(), slug)
		})
	}
	wg.Wait()
	close(errs)

	for err := range errs {
		if err != nil {
			t.Errorf("concurrent Bootstrap() error: %v", err)
		}
	}
}

func TestSeedBuiltinRoles_CreatesExactlyThreeImmutableRoles(t *testing.T) {
	store, conn, slug := openTestStore(t)
	schema := tenantschema.Name(slug)

	if err := store.SeedBuiltinRoles(context.Background(), slug); err != nil {
		t.Fatalf("SeedBuiltinRoles() error: %v", err)
	}

	for _, name := range []string{"admin", "user", "portal"} {
		var isImmutable bool
		err := conn.QueryRowContext(context.Background(),
			fmt.Sprintf("SELECT is_immutable FROM %s.roles WHERE name = $1", schema), name,
		).Scan(&isImmutable)
		if err != nil {
			t.Errorf("role %q not found after seeding: %v", name, err)
			continue
		}
		if !isImmutable {
			t.Errorf("role %q: is_immutable = false, want true", name)
		}
	}

	var count int
	if err := conn.QueryRowContext(context.Background(),
		fmt.Sprintf("SELECT count(*) FROM %s.roles", schema),
	).Scan(&count); err != nil {
		t.Fatalf("count roles: %v", err)
	}
	if count != 3 {
		t.Errorf("role count = %d, want 3", count)
	}
}

func TestSeedBuiltinRoles_IsIdempotent(t *testing.T) {
	store, conn, slug := openTestStore(t)
	schema := tenantschema.Name(slug)

	if err := store.SeedBuiltinRoles(context.Background(), slug); err != nil {
		t.Fatalf("first SeedBuiltinRoles() error: %v", err)
	}
	if err := store.SeedBuiltinRoles(context.Background(), slug); err != nil {
		t.Fatalf("second SeedBuiltinRoles() error: %v", err)
	}

	var count int
	if err := conn.QueryRowContext(context.Background(),
		fmt.Sprintf("SELECT count(*) FROM %s.roles", schema),
	).Scan(&count); err != nil {
		t.Fatalf("count roles: %v", err)
	}
	if count != 3 {
		t.Errorf("role count after seeding twice = %d, want 3", count)
	}
}

func TestGetRoleByName_ResolvesSeededRole(t *testing.T) {
	store, conn, slug := openTestStore(t)
	schema := tenantschema.Name(slug)

	if err := store.SeedBuiltinRoles(context.Background(), slug); err != nil {
		t.Fatalf("SeedBuiltinRoles() error: %v", err)
	}

	gotID, err := store.GetRoleByName(context.Background(), slug, "admin")
	if err != nil {
		t.Fatalf("GetRoleByName() error: %v", err)
	}

	var wantID string
	if err := conn.QueryRowContext(context.Background(),
		fmt.Sprintf("SELECT id FROM %s.roles WHERE name = 'admin'", schema),
	).Scan(&wantID); err != nil {
		t.Fatalf("query admin role id: %v", err)
	}

	if gotID != wantID {
		t.Errorf("GetRoleByName() = %q, want %q", gotID, wantID)
	}
}

func TestGetRoleByName_UnseededNameReturnsErrRoleNotFound(t *testing.T) {
	store, _, slug := openTestStore(t)

	_, err := store.GetRoleByName(context.Background(), slug, "does-not-exist")
	if !errors.Is(err, ErrRoleNotFound) {
		t.Errorf("GetRoleByName() error = %v, want ErrRoleNotFound", err)
	}
}

func TestRolePermissionsAndUserRoles_TablesAcceptRows(t *testing.T) {
	store, conn, slug := openTestStore(t)
	schema := tenantschema.Name(slug)
	ctx := context.Background()

	if err := store.SeedBuiltinRoles(ctx, slug); err != nil {
		t.Fatalf("SeedBuiltinRoles() error: %v", err)
	}
	adminID, err := store.GetRoleByName(ctx, slug, "admin")
	if err != nil {
		t.Fatalf("GetRoleByName() error: %v", err)
	}

	if _, err := conn.ExecContext(ctx,
		fmt.Sprintf("INSERT INTO %s.role_permissions (role_id, permission_name) VALUES ($1, $2)", schema),
		adminID, "contacts:contact:read",
	); err != nil {
		t.Fatalf("insert role_permissions row: %v", err)
	}

	fakeUserID := "00000000-0000-0000-0000-000000000001"
	if _, err := conn.ExecContext(ctx,
		fmt.Sprintf("INSERT INTO %s.user_roles (user_id, role_id) VALUES ($1, $2)", schema),
		fakeUserID, adminID,
	); err != nil {
		t.Fatalf("insert user_roles row: %v", err)
	}

	// Deleting the role cascades to both — ON DELETE CASCADE on the FK.
	if _, err := conn.ExecContext(ctx, fmt.Sprintf("DELETE FROM %s.roles WHERE id = $1", schema), adminID); err != nil {
		t.Fatalf("delete role: %v", err)
	}

	var count int
	if err := conn.QueryRowContext(ctx,
		fmt.Sprintf("SELECT count(*) FROM %s.role_permissions WHERE role_id = $1", schema), adminID,
	).Scan(&count); err != nil {
		t.Fatalf("count role_permissions: %v", err)
	}
	if count != 0 {
		t.Errorf("role_permissions rows after cascading delete = %d, want 0", count)
	}
}

func TestCountUsers_CountsDistinctUsersAcrossRoles(t *testing.T) {
	store, conn, slug := openTestStore(t)
	schema := tenantschema.Name(slug)
	ctx := context.Background()

	if err := store.SeedBuiltinRoles(ctx, slug); err != nil {
		t.Fatalf("SeedBuiltinRoles() error: %v", err)
	}
	adminID, err := store.GetRoleByName(ctx, slug, "admin")
	if err != nil {
		t.Fatalf("GetRoleByName(admin) error: %v", err)
	}
	userRoleID, err := store.GetRoleByName(ctx, slug, "user")
	if err != nil {
		t.Fatalf("GetRoleByName(user) error: %v", err)
	}

	userA := "00000000-0000-0000-0000-0000000000a1"
	userB := "00000000-0000-0000-0000-0000000000b2"
	rows := []struct{ userID, roleID string }{
		{userA, adminID},
		{userA, userRoleID}, // same user, second role — must not double-count
		{userB, userRoleID},
	}
	for _, r := range rows {
		if _, err := conn.ExecContext(ctx,
			fmt.Sprintf("INSERT INTO %s.user_roles (user_id, role_id) VALUES ($1, $2)", schema),
			r.userID, r.roleID,
		); err != nil {
			t.Fatalf("insert user_roles row: %v", err)
		}
	}

	got, err := store.CountUsers(ctx, slug)
	if err != nil {
		t.Fatalf("CountUsers() error: %v", err)
	}
	if got != 2 {
		t.Errorf("CountUsers() = %d, want 2", got)
	}
}

func TestCountUsers_UnprovisionedTenantReturnsZero(t *testing.T) {
	store, _, _ := openTestStore(t)

	got, err := store.CountUsers(context.Background(), fmt.Sprintf("nosuchtenant%d", time.Now().UnixNano()))
	if err != nil {
		t.Fatalf("CountUsers() error: %v", err)
	}
	if got != 0 {
		t.Errorf("CountUsers() = %d, want 0", got)
	}
}

func TestIsMember_TrueForGrantedUser(t *testing.T) {
	store, conn, slug := openTestStore(t)
	schema := tenantschema.Name(slug)
	ctx := context.Background()

	if err := store.SeedBuiltinRoles(ctx, slug); err != nil {
		t.Fatalf("SeedBuiltinRoles() error: %v", err)
	}
	roleID, err := store.GetRoleByName(ctx, slug, "user")
	if err != nil {
		t.Fatalf("GetRoleByName() error: %v", err)
	}
	userID := "00000000-0000-0000-0000-000000000002"
	if _, err := conn.ExecContext(ctx,
		fmt.Sprintf("INSERT INTO %s.user_roles (user_id, role_id) VALUES ($1, $2)", schema), userID, roleID,
	); err != nil {
		t.Fatalf("insert user_roles row: %v", err)
	}

	isMember, err := store.IsMember(ctx, slug, userID)
	if err != nil {
		t.Fatalf("IsMember() error: %v", err)
	}
	if !isMember {
		t.Error("IsMember() = false, want true")
	}
}

func TestIsMember_FalseForUngranted(t *testing.T) {
	store, _, slug := openTestStore(t)

	isMember, err := store.IsMember(context.Background(), slug, "00000000-0000-0000-0000-000000000003")
	if err != nil {
		t.Fatalf("IsMember() error: %v", err)
	}
	if isMember {
		t.Error("IsMember() = true for a user with no grants, want false")
	}
}

func TestIsMember_FalseForExpiredGrant(t *testing.T) {
	store, conn, slug := openTestStore(t)
	schema := tenantschema.Name(slug)
	ctx := context.Background()

	if err := store.SeedBuiltinRoles(ctx, slug); err != nil {
		t.Fatalf("SeedBuiltinRoles() error: %v", err)
	}
	roleID, err := store.GetRoleByName(ctx, slug, "user")
	if err != nil {
		t.Fatalf("GetRoleByName() error: %v", err)
	}
	userID := "00000000-0000-0000-0000-000000000004"
	if _, err := conn.ExecContext(ctx,
		fmt.Sprintf("INSERT INTO %s.user_roles (user_id, role_id, expires_at) VALUES ($1, $2, NOW() - interval '1 hour')", schema), userID, roleID,
	); err != nil {
		t.Fatalf("insert expired user_roles row: %v", err)
	}

	isMember, err := store.IsMember(ctx, slug, userID)
	if err != nil {
		t.Fatalf("IsMember() error: %v", err)
	}
	if isMember {
		t.Error("IsMember() = true for an expired grant, want false")
	}
}

func TestIsMember_UnprovisionedTenantReturnsFalse(t *testing.T) {
	slug := fmt.Sprintf("roletest-unprovisioned-%d", time.Now().UnixNano())
	conn, err := db.New(localPostgresDSN)
	if err != nil {
		t.Skipf("postgres not reachable at %s (start compose.dev.yml): %v", localPostgresDSN, err)
	}
	defer func() { _ = conn.Close() }()
	store := NewStore(conn)

	isMember, err := store.IsMember(context.Background(), slug, "00000000-0000-0000-0000-000000000005")
	if err != nil {
		t.Fatalf("IsMember() error: %v", err)
	}
	if isMember {
		t.Error("IsMember() = true for an unprovisioned tenant, want false")
	}
}

func TestPermissionNamesForUser_ReturnsDistinctGrantedPermissions(t *testing.T) {
	store, conn, slug := openTestStore(t)
	schema := tenantschema.Name(slug)
	ctx := context.Background()

	if err := store.SeedBuiltinRoles(ctx, slug); err != nil {
		t.Fatalf("SeedBuiltinRoles() error: %v", err)
	}
	roleID, err := store.GetRoleByName(ctx, slug, "admin")
	if err != nil {
		t.Fatalf("GetRoleByName() error: %v", err)
	}
	for _, perm := range []string{"widgets.read", "widgets.write"} {
		if _, err := conn.ExecContext(ctx,
			fmt.Sprintf("INSERT INTO %s.role_permissions (role_id, permission_name) VALUES ($1, $2)", schema), roleID, perm,
		); err != nil {
			t.Fatalf("insert role_permissions row: %v", err)
		}
	}
	userID := "00000000-0000-0000-0000-000000000006"
	if _, err := conn.ExecContext(ctx,
		fmt.Sprintf("INSERT INTO %s.user_roles (user_id, role_id) VALUES ($1, $2)", schema), userID, roleID,
	); err != nil {
		t.Fatalf("insert user_roles row: %v", err)
	}

	names, err := store.PermissionNamesForUser(ctx, slug, userID)
	if err != nil {
		t.Fatalf("PermissionNamesForUser() error: %v", err)
	}
	if len(names) != 2 || names[0] != "widgets.read" || names[1] != "widgets.write" {
		t.Errorf("PermissionNamesForUser() = %v, want [widgets.read widgets.write]", names)
	}
}

func TestPermissionNamesForUser_EmptyForUngranted(t *testing.T) {
	store, _, slug := openTestStore(t)

	names, err := store.PermissionNamesForUser(context.Background(), slug, "00000000-0000-0000-0000-000000000007")
	if err != nil {
		t.Fatalf("PermissionNamesForUser() error: %v", err)
	}
	if len(names) != 0 {
		t.Errorf("PermissionNamesForUser() = %v, want empty", names)
	}
}

func TestAdminUserID_ReturnsEarliestAdminGrant(t *testing.T) {
	store, conn, slug := openTestStore(t)
	schema := tenantschema.Name(slug)
	ctx := context.Background()

	if err := store.SeedBuiltinRoles(ctx, slug); err != nil {
		t.Fatalf("SeedBuiltinRoles() error: %v", err)
	}
	adminID, err := store.GetRoleByName(ctx, slug, "admin")
	if err != nil {
		t.Fatalf("GetRoleByName(admin) error: %v", err)
	}

	firstAdmin := "00000000-0000-0000-0000-0000000000c1"
	secondAdmin := "00000000-0000-0000-0000-0000000000c2"

	if _, err := conn.ExecContext(ctx,
		fmt.Sprintf("INSERT INTO %s.user_roles (user_id, role_id, granted_at) VALUES ($1, $2, NOW() - interval '1 hour')", schema),
		firstAdmin, adminID,
	); err != nil {
		t.Fatalf("insert first admin grant: %v", err)
	}
	if _, err := conn.ExecContext(ctx,
		fmt.Sprintf("INSERT INTO %s.user_roles (user_id, role_id, granted_at) VALUES ($1, $2, NOW())", schema),
		secondAdmin, adminID,
	); err != nil {
		t.Fatalf("insert second admin grant: %v", err)
	}

	got, err := store.AdminUserID(ctx, slug)
	if err != nil {
		t.Fatalf("AdminUserID() error: %v", err)
	}
	if got != firstAdmin {
		t.Errorf("AdminUserID() = %q, want %q (earliest grant)", got, firstAdmin)
	}
}

func TestAdminUserID_NoAdminGrantReturnsErrAdminUserNotFound(t *testing.T) {
	store, _, slug := openTestStore(t)

	if err := store.SeedBuiltinRoles(context.Background(), slug); err != nil {
		t.Fatalf("SeedBuiltinRoles() error: %v", err)
	}

	_, err := store.AdminUserID(context.Background(), slug)
	if !errors.Is(err, ErrAdminUserNotFound) {
		t.Errorf("AdminUserID() error = %v, want ErrAdminUserNotFound", err)
	}
}

func TestAdminUserID_UnprovisionedTenantReturnsErrAdminUserNotFound(t *testing.T) {
	store, _, _ := openTestStore(t)

	_, err := store.AdminUserID(context.Background(), fmt.Sprintf("nosuchtenant%d", time.Now().UnixNano()))
	if !errors.Is(err, ErrAdminUserNotFound) {
		t.Errorf("AdminUserID() error = %v, want ErrAdminUserNotFound", err)
	}
}

func TestRoleIDsForUser_ReturnsUnexpiredRoleIDs(t *testing.T) {
	store, conn, slug := openTestStore(t)
	schema := tenantschema.Name(slug)
	ctx := context.Background()

	if err := store.SeedBuiltinRoles(ctx, slug); err != nil {
		t.Fatalf("SeedBuiltinRoles() error: %v", err)
	}
	adminID, err := store.GetRoleByName(ctx, slug, "admin")
	if err != nil {
		t.Fatalf("GetRoleByName(admin) error: %v", err)
	}
	userID, err := store.GetRoleByName(ctx, slug, "user")
	if err != nil {
		t.Fatalf("GetRoleByName(user) error: %v", err)
	}

	grantee := "00000000-0000-0000-0000-000000000008"
	if _, err := conn.ExecContext(ctx,
		fmt.Sprintf("INSERT INTO %s.user_roles (user_id, role_id) VALUES ($1, $2)", schema), grantee, adminID,
	); err != nil {
		t.Fatalf("insert unexpired grant: %v", err)
	}
	if _, err := conn.ExecContext(ctx,
		fmt.Sprintf("INSERT INTO %s.user_roles (user_id, role_id, expires_at) VALUES ($1, $2, NOW() - interval '1 hour')", schema), grantee, userID,
	); err != nil {
		t.Fatalf("insert expired grant: %v", err)
	}

	ids, err := store.RoleIDsForUser(ctx, slug, grantee)
	if err != nil {
		t.Fatalf("RoleIDsForUser() error: %v", err)
	}
	if len(ids) != 1 || ids[0] != adminID {
		t.Errorf("RoleIDsForUser() = %v, want [%s]", ids, adminID)
	}
}

func TestRoleIDsForUser_UnprovisionedTenantReturnsEmpty(t *testing.T) {
	store, _, _ := openTestStore(t)

	ids, err := store.RoleIDsForUser(context.Background(), fmt.Sprintf("nosuchtenant%d", time.Now().UnixNano()), "u1")
	if err != nil {
		t.Fatalf("RoleIDsForUser() error: %v", err)
	}
	if len(ids) != 0 {
		t.Errorf("RoleIDsForUser() = %v, want empty", ids)
	}
}

func TestAllRoles_ReturnsEveryRoleWithParentID(t *testing.T) {
	store, conn, slug := openTestStore(t)
	schema := tenantschema.Name(slug)
	ctx := context.Background()

	if err := store.SeedBuiltinRoles(ctx, slug); err != nil {
		t.Fatalf("SeedBuiltinRoles() error: %v", err)
	}
	adminID, err := store.GetRoleByName(ctx, slug, "admin")
	if err != nil {
		t.Fatalf("GetRoleByName(admin) error: %v", err)
	}

	var childID string
	if err := conn.QueryRowContext(ctx,
		fmt.Sprintf("INSERT INTO %s.roles (name, parent_id) VALUES ('sales_manager', $1) RETURNING id", schema), adminID,
	).Scan(&childID); err != nil {
		t.Fatalf("insert child role: %v", err)
	}

	roles, err := store.AllRoles(ctx, slug)
	if err != nil {
		t.Fatalf("AllRoles() error: %v", err)
	}

	byID := make(map[string]Role, len(roles))
	for _, r := range roles {
		byID[r.ID] = r
	}

	admin, ok := byID[adminID]
	if !ok {
		t.Fatal("expected the admin role in AllRoles() result")
	}
	if admin.ParentID != nil {
		t.Errorf("admin.ParentID = %v, want nil", admin.ParentID)
	}

	child, ok := byID[childID]
	if !ok {
		t.Fatal("expected the sales_manager role in AllRoles() result")
	}
	if child.ParentID == nil || *child.ParentID != adminID {
		t.Errorf("child.ParentID = %v, want %q", child.ParentID, adminID)
	}
	if child.Name != "sales_manager" {
		t.Errorf("child.Name = %q, want %q", child.Name, "sales_manager")
	}
}

func TestAllRoles_UnprovisionedTenantReturnsEmpty(t *testing.T) {
	store, _, _ := openTestStore(t)

	roles, err := store.AllRoles(context.Background(), fmt.Sprintf("nosuchtenant%d", time.Now().UnixNano()))
	if err != nil {
		t.Fatalf("AllRoles() error: %v", err)
	}
	if len(roles) != 0 {
		t.Errorf("AllRoles() = %v, want empty", roles)
	}
}

func TestAllRolePermissions_ReturnsGrantsKeyedByRoleID(t *testing.T) {
	store, conn, slug := openTestStore(t)
	schema := tenantschema.Name(slug)
	ctx := context.Background()

	if err := store.SeedBuiltinRoles(ctx, slug); err != nil {
		t.Fatalf("SeedBuiltinRoles() error: %v", err)
	}
	adminID, err := store.GetRoleByName(ctx, slug, "admin")
	if err != nil {
		t.Fatalf("GetRoleByName(admin) error: %v", err)
	}
	userID, err := store.GetRoleByName(ctx, slug, "user")
	if err != nil {
		t.Fatalf("GetRoleByName(user) error: %v", err)
	}

	for _, grant := range []struct{ roleID, perm string }{
		{adminID, "widgets.read"},
		{adminID, "widgets.write"},
		{userID, "widgets.read"},
	} {
		if _, err := conn.ExecContext(ctx,
			fmt.Sprintf("INSERT INTO %s.role_permissions (role_id, permission_name) VALUES ($1, $2)", schema), grant.roleID, grant.perm,
		); err != nil {
			t.Fatalf("insert role_permissions row: %v", err)
		}
	}

	byRole, err := store.AllRolePermissions(ctx, slug)
	if err != nil {
		t.Fatalf("AllRolePermissions() error: %v", err)
	}

	if len(byRole[adminID]) != 2 {
		t.Errorf("byRole[admin] = %v, want 2 permissions", byRole[adminID])
	}
	if len(byRole[userID]) != 1 || byRole[userID][0] != "widgets.read" {
		t.Errorf("byRole[user] = %v, want [widgets.read]", byRole[userID])
	}
}

func TestAllRolePermissions_UnprovisionedTenantReturnsEmpty(t *testing.T) {
	store, _, _ := openTestStore(t)

	byRole, err := store.AllRolePermissions(context.Background(), fmt.Sprintf("nosuchtenant%d", time.Now().UnixNano()))
	if err != nil {
		t.Fatalf("AllRolePermissions() error: %v", err)
	}
	if len(byRole) != 0 {
		t.Errorf("AllRolePermissions() = %v, want empty", byRole)
	}
}

func TestAssignRole_GrantsAndIsReflectedInMembership(t *testing.T) {
	store, _, slug := openTestStore(t)
	ctx := context.Background()

	if err := store.SeedBuiltinRoles(ctx, slug); err != nil {
		t.Fatalf("SeedBuiltinRoles() error: %v", err)
	}
	roleID, err := store.GetRoleByName(ctx, slug, "user")
	if err != nil {
		t.Fatalf("GetRoleByName() error: %v", err)
	}
	userID := "00000000-0000-0000-0000-000000000010"
	grantedBy := "00000000-0000-0000-0000-000000000011"

	if err := store.AssignRole(ctx, slug, userID, roleID, grantedBy); err != nil {
		t.Fatalf("AssignRole() error: %v", err)
	}

	isMember, err := store.IsMember(ctx, slug, userID)
	if err != nil {
		t.Fatalf("IsMember() error: %v", err)
	}
	if !isMember {
		t.Error("IsMember() = false after AssignRole(), want true")
	}
	names, err := store.RoleNamesForUser(ctx, slug, userID)
	if err != nil {
		t.Fatalf("RoleNamesForUser() error: %v", err)
	}
	if len(names) != 1 || names[0] != "user" {
		t.Errorf("RoleNamesForUser() = %v, want [user]", names)
	}
}

func TestAssignRole_AlreadyGrantedIsANoOpNotAnError(t *testing.T) {
	store, _, slug := openTestStore(t)
	ctx := context.Background()

	if err := store.SeedBuiltinRoles(ctx, slug); err != nil {
		t.Fatalf("SeedBuiltinRoles() error: %v", err)
	}
	roleID, err := store.GetRoleByName(ctx, slug, "user")
	if err != nil {
		t.Fatalf("GetRoleByName() error: %v", err)
	}
	userID := "00000000-0000-0000-0000-000000000012"

	if err := store.AssignRole(ctx, slug, userID, roleID, "00000000-0000-0000-0000-000000000099"); err != nil {
		t.Fatalf("first AssignRole() error: %v", err)
	}
	if err := store.AssignRole(ctx, slug, userID, roleID, "00000000-0000-0000-0000-000000000099"); err != nil {
		t.Fatalf("second AssignRole() (already granted) error: %v", err)
	}

	names, err := store.RoleNamesForUser(ctx, slug, userID)
	if err != nil {
		t.Fatalf("RoleNamesForUser() error: %v", err)
	}
	if len(names) != 1 {
		t.Errorf("RoleNamesForUser() = %v, want exactly one entry (idempotent grant)", names)
	}
}

func TestRevokeRole_RemovesGrant(t *testing.T) {
	store, _, slug := openTestStore(t)
	ctx := context.Background()

	if err := store.SeedBuiltinRoles(ctx, slug); err != nil {
		t.Fatalf("SeedBuiltinRoles() error: %v", err)
	}
	roleID, err := store.GetRoleByName(ctx, slug, "user")
	if err != nil {
		t.Fatalf("GetRoleByName() error: %v", err)
	}
	userID := "00000000-0000-0000-0000-000000000013"
	if err := store.AssignRole(ctx, slug, userID, roleID, "00000000-0000-0000-0000-000000000099"); err != nil {
		t.Fatalf("AssignRole() error: %v", err)
	}

	if err := store.RevokeRole(ctx, slug, userID, roleID); err != nil {
		t.Fatalf("RevokeRole() error: %v", err)
	}

	isMember, err := store.IsMember(ctx, slug, userID)
	if err != nil {
		t.Fatalf("IsMember() error: %v", err)
	}
	if isMember {
		t.Error("IsMember() = true after RevokeRole(), want false")
	}
}

func TestRevokeRole_UngrantedIsANoOpNotAnError(t *testing.T) {
	store, _, slug := openTestStore(t)
	ctx := context.Background()

	if err := store.SeedBuiltinRoles(ctx, slug); err != nil {
		t.Fatalf("SeedBuiltinRoles() error: %v", err)
	}
	roleID, err := store.GetRoleByName(ctx, slug, "user")
	if err != nil {
		t.Fatalf("GetRoleByName() error: %v", err)
	}

	if err := store.RevokeRole(ctx, slug, "00000000-0000-0000-0000-000000000014", roleID); err != nil {
		t.Errorf("RevokeRole() on an ungranted role error: %v, want nil", err)
	}
}
