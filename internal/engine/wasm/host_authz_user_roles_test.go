package wasm

import (
	"database/sql"
	"fmt"
	"slices"
	"testing"

	abiv1 "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/djangbahevans/goerp/internal/engine/tenantschema"
)

const (
	adminRoleID   = "0aaaaaaa-0000-0000-0000-000000000001"
	viewerRoleID  = "0aaaaaaa-0000-0000-0000-000000000002"
	expiredRoleID = "0aaaaaaa-0000-0000-0000-000000000003"
	otherRoleID   = "0aaaaaaa-0000-0000-0000-000000000004"
)

// seedRoles creates the engine's roles and user_roles tables in the fixture
// tenant schema and grants policyUserID the admin and viewer roles, an
// expired role, and policyOtherID a role of its own.
func seedRoles(t *testing.T, db *sql.DB, slug string) {
	t.Helper()
	schema := tenantschema.Name(slug)
	stmts := []string{
		fmt.Sprintf(`CREATE TABLE %s.roles (id uuid PRIMARY KEY, name text NOT NULL UNIQUE, is_immutable boolean NOT NULL DEFAULT false)`, schema),
		fmt.Sprintf(`CREATE TABLE %s.user_roles (user_id uuid NOT NULL, role_id uuid NOT NULL REFERENCES %s.roles(id), expires_at timestamptz, PRIMARY KEY (user_id, role_id))`, schema, schema),
		fmt.Sprintf(`INSERT INTO %s.roles VALUES ('%s', 'admin', true), ('%s', 'viewer', false), ('%s', 'temp', false), ('%s', 'auditor', false)`,
			schema, adminRoleID, viewerRoleID, expiredRoleID, otherRoleID),
		fmt.Sprintf(`INSERT INTO %s.user_roles VALUES ('%s', '%s', NULL), ('%s', '%s', NOW() + interval '1 day'), ('%s', '%s', NOW() - interval '1 day'), ('%s', '%s', NULL)`,
			schema, policyUserID, adminRoleID, policyUserID, viewerRoleID, policyUserID, expiredRoleID, policyOtherID, otherRoleID),
	}
	for _, stmt := range stmts {
		if _, err := db.ExecContext(t.Context(), stmt); err != nil {
			t.Fatalf("%s: %v", stmt, err)
		}
	}
}

func TestUserRoles_ListsTheCallersCurrentRolesByName(t *testing.T) {
	db, slug := newInvoiceFixture(t)
	seedRoles(t, db, slug)
	mc := invoiceModuleContext(slug)

	got, err := userRoles(t.Context(), db, mc)

	if err != nil {
		t.Fatalf("userRoles: %v", err)
	}
	want := []abiv1.AuthzRole{
		{ID: adminRoleID, Name: "admin", IsSystem: true},
		{ID: viewerRoleID, Name: "viewer", IsSystem: false},
	}
	if !slices.Equal(got, want) {
		t.Errorf("userRoles = %+v, want %+v (an expired grant and another user's role excluded)", got, want)
	}
}

func TestUserRoles_AUserWithNoRolesGetsAnEmptyList(t *testing.T) {
	db, slug := newInvoiceFixture(t)
	seedRoles(t, db, slug)
	mc := invoiceModuleContext(slug)
	mc.UserID = "cccccccc-cccc-cccc-cccc-cccccccccccc"

	got, err := userRoles(t.Context(), db, mc)

	if err != nil || got == nil || len(got) != 0 {
		t.Errorf("userRoles = %+v, %v, want an empty non-nil list", got, err)
	}
}

func TestUserRoles_AContextWithNoUserHoldsNone(t *testing.T) {
	db, slug := newInvoiceFixture(t)
	seedRoles(t, db, slug)
	mc := invoiceModuleContext(slug)
	mc.UserID = ""

	got, err := userRoles(t.Context(), db, mc)

	if err != nil || got == nil || len(got) != 0 {
		t.Errorf("userRoles = %+v, %v, want an empty non-nil list", got, err)
	}
}
