package role

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
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
