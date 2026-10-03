// Package mfatest creates isolated tenant membership fixtures for MFA tests.
package mfatest

import (
	"database/sql"
	"fmt"
	"testing"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/role"
	"github.com/djangbahevans/goerp/internal/engine/tenant"
	"github.com/djangbahevans/goerp/internal/engine/tenantschema"
)

type Tenant struct {
	ID   string
	Slug string
}

func NewTenant(t *testing.T, conn *sql.DB) Tenant {
	t.Helper()
	store := tenant.NewStore(conn)
	if err := store.Bootstrap(t.Context()); err != nil {
		t.Fatal(err)
	}

	tt, err := store.CreateTenant(t.Context(), fmt.Sprintf("mfatest%d", time.Now().UnixNano()), "MFA test tenant")
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() {
		_, _ = conn.Exec(`DELETE FROM system.tenants WHERE id = $1`, tt.ID)
	})
	result := Tenant{ID: tt.ID, Slug: tt.Slug}
	CreateMembers(t, conn, result)
	return result
}

func CreateMembers(t *testing.T, conn *sql.DB, tt Tenant) {
	t.Helper()
	schema := tenantschema.Name(tt.Slug)
	if _, err := conn.ExecContext(t.Context(), "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() {
		_, _ = conn.Exec("DROP SCHEMA " + schema + " CASCADE")
	})
	if err := role.NewStore(conn).Bootstrap(t.Context(), tt.Slug); err != nil {
		t.Fatal(err)
	}
}

func AddMember(t *testing.T, conn *sql.DB, tt Tenant, userID string) {
	t.Helper()
	if err := role.NewStore(conn).AddMember(t.Context(), tt.Slug, userID); err != nil {
		t.Fatal(err)
	}
}
