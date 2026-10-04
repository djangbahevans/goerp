// Package membershiptest initializes private databases with production
// membership ownership for integration fixtures.
package membershiptest

import (
	"context"
	"database/sql"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"uuid"

	"github.com/djangbahevans/goerp/internal/engine/db"
	"github.com/djangbahevans/goerp/internal/engine/role"
	"github.com/djangbahevans/goerp/internal/engine/tenant"
	"github.com/djangbahevans/goerp/internal/engine/user"
)

func NewUnbootstrappedDB(t *testing.T) *sql.DB {
	t.Helper()
	ctx := t.Context()
	admin, err := db.New("postgres://goerp:dev@localhost:15432/goerp")
	if err != nil {
		t.Skipf("dev Postgres unavailable: %v", err)
	}

	t.Cleanup(func() { _ = admin.Close() })
	name := "membership" + strings.ReplaceAll(uuid.NewV7().String(), "-", "")
	if _, err := admin.ExecContext(ctx, "CREATE DATABASE "+name); err != nil {
		t.Fatalf("create private membership database: %v", err)
	}

	t.Cleanup(func() {
		if _, err := admin.ExecContext(context.Background(), "DROP DATABASE "+name); err != nil {
			t.Errorf("drop private membership database: %v", err)
		}
	})

	conn := Open(t, name, "goerp")
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate membership fixture source")
	}

	setupPath := filepath.Join(filepath.Dir(source), "../../../../..", "docker/postgres-initdb/database/setup.sql")
	setup, err := os.ReadFile(setupPath)
	if err != nil {
		t.Fatalf("read database setup: %v", err)
	}

	if _, err := conn.ExecContext(ctx, strings.ReplaceAll(string(setup), `:"DBNAME"`, name)); err != nil {
		t.Fatalf("initialize private membership database: %v", err)
	}

	return conn
}

func New(t *testing.T) *sql.DB {
	t.Helper()
	conn := NewUnbootstrappedDB(t)
	var name string
	if err := conn.QueryRowContext(t.Context(), `SELECT current_database()`).Scan(&name); err != nil {
		t.Fatal(err)
	}

	schemaSync := Open(t, name, "schema_sync_user")
	for _, bootstrap := range []func(context.Context) error{
		tenant.NewStore(schemaSync).Bootstrap,
		user.NewStore(schemaSync).Bootstrap,
		role.NewStore(schemaSync).BootstrapMembershipIndex,
	} {
		if err := bootstrap(t.Context()); err != nil {
			t.Fatalf("bootstrap private membership database: %v", err)
		}
	}

	return conn
}

func Open(t *testing.T, database, roleName string) *sql.DB {
	t.Helper()
	dsn := url.URL{
		Scheme: "postgres",
		User:   url.UserPassword(roleName, "dev"),
		Host:   "localhost:15432",
		Path:   "/" + database,
	}
	conn, err := db.New(dsn.String())
	if err != nil {
		t.Fatalf("connect to private membership database as %s: %v", roleName, err)
	}

	t.Cleanup(func() { _ = conn.Close() })

	return conn
}
