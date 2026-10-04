package role_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/auth/membership/membershiptest"
	"github.com/djangbahevans/goerp/internal/engine/role"
	"github.com/djangbahevans/goerp/internal/engine/tenant"
	"github.com/djangbahevans/goerp/internal/engine/tenantschema"
	"github.com/djangbahevans/goerp/internal/engine/user"
	"github.com/jackc/pgx/v5/pgconn"
)

type membershipQuerier interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func openOwnershipPools(t *testing.T, admin *sql.DB) (schemaSync, engine *sql.DB) {
	t.Helper()
	var name string
	if err := admin.QueryRowContext(t.Context(), `SELECT current_database()`).Scan(&name); err != nil {
		t.Fatal(err)
	}

	return membershiptest.Open(t, name, "schema_sync_user"), membershiptest.Open(t, name, "engine_user")
}

func requireMembershipOwners(t *testing.T, conn *sql.DB) {
	t.Helper()
	for _, query := range []string{
		`SELECT pg_get_userbyid(relowner) FROM pg_class WHERE oid = 'system.tenant_memberships'::regclass`,
		`SELECT pg_get_userbyid(proowner) FROM pg_proc WHERE oid = 'system.sync_tenant_membership()'::regprocedure`,
	} {
		var owner string
		if err := conn.QueryRowContext(t.Context(), query).Scan(&owner); err != nil {
			t.Fatal(err)
		}

		if owner != "schema_sync_user" {
			t.Fatalf("membership owner = %q, want schema_sync_user", owner)
		}
	}
}

func TestMembershipBootstrap_OwnershipAndOrder(t *testing.T) {
	for _, order := range []struct {
		triggerFirst    bool
		schemaSyncFirst bool
	}{
		{false, false}, {true, false}, {false, true}, {true, true},
	} {
		t.Run(fmt.Sprintf("trigger_first_%t_schema_sync_first_%t", order.triggerFirst, order.schemaSyncFirst), func(t *testing.T) {
			admin := membershiptest.NewUnbootstrappedDB(t)
			schemaSync, _ := openOwnershipPools(t, admin)
			admin.SetMaxOpenConns(1)
			schemaSync.SetMaxOpenConns(1)
			ctx := t.Context()
			for _, bootstrap := range []func(context.Context) error{
				tenant.NewStore(schemaSync).Bootstrap,
				user.NewStore(schemaSync).Bootstrap,
			} {
				if err := bootstrap(ctx); err != nil {
					t.Fatal(err)
				}
			}

			slug := "ownershiptest"
			if _, err := schemaSync.ExecContext(ctx, `CREATE SCHEMA tenant_ownershiptest`); err != nil {
				t.Fatal(err)
			}

			if err := role.NewStore(schemaSync).Bootstrap(ctx, slug); err != nil {
				t.Fatal(err)
			}

			pools := []*sql.DB{admin, schemaSync, admin, schemaSync}
			if order.schemaSyncFirst {
				pools = []*sql.DB{schemaSync, admin, schemaSync, admin}
			}

			for _, pool := range pools {
				store := role.NewStore(pool)
				if order.triggerFirst {
					if err := store.AttachMembershipTrigger(ctx, slug); err != nil {
						t.Fatal(err)
					}
				}

				if err := store.BootstrapMembershipIndex(ctx); err != nil {
					t.Fatal(err)
				}

				if err := store.AttachMembershipTrigger(ctx, slug); err != nil {
					t.Fatal(err)
				}

				requireMembershipOwners(t, admin)
				var current string
				if err := pool.QueryRowContext(ctx, `SELECT current_user`).Scan(&current); err != nil {
					t.Fatal(err)
				}

				want := "schema_sync_user"
				if pool == admin {
					want = "goerp"
				}

				if current != want {
					t.Fatalf("pooled role = %s, want %s", current, want)
				}
			}
		})
	}
}

func TestMembershipBootstrap_RejectsIncompatibleOwners(t *testing.T) {
	for _, object := range []string{"TABLE system.tenant_memberships", "FUNCTION system.sync_tenant_membership()"} {
		t.Run(object, func(t *testing.T) {
			admin := membershiptest.New(t)
			schemaSync, _ := openOwnershipPools(t, admin)
			if _, err := admin.ExecContext(t.Context(), "ALTER "+object+" OWNER TO goerp"); err != nil {
				t.Fatal(err)
			}

			if strings.HasPrefix(object, "TABLE") {
				var writable bool
				if err := admin.QueryRowContext(t.Context(), `SELECT has_table_privilege('schema_sync_user', 'system.tenant_memberships', 'INSERT,DELETE')`).Scan(&writable); err != nil {
					t.Fatal(err)
				}

				if writable {
					t.Fatal("incompatible table fixture permits definer writes")
				}
			}

			for _, pool := range []*sql.DB{admin, schemaSync} {
				store := role.NewStore(pool)
				for _, bootstrap := range []func() error{
					func() error { return store.BootstrapMembershipIndex(t.Context()) },
					func() error { return store.AttachMembershipTrigger(t.Context(), "ownershiptest") },
				} {
					err := bootstrap()
					if err == nil || !strings.Contains(err.Error(), "owned by goerp; expected schema_sync_user") || !strings.Contains(err.Error(), "database administrator") {
						t.Fatalf("incompatible ownership error = %v", err)
					}
				}
			}
		})
	}
}

func TestMembershipTrigger_PrimaryTransactionsAndIsolation(t *testing.T) {
	admin := membershiptest.New(t)
	schemaSync, engine := openOwnershipPools(t, admin)
	ctx := t.Context()
	slug := fmt.Sprintf("membership%d", time.Now().UnixNano())
	schema := tenantschema.Name(slug)
	tenantStore := tenant.NewStore(schemaSync)
	tt, err := tenantStore.CreateTenant(ctx, slug, "Ownership test")
	if err != nil {
		t.Fatal(err)
	}

	if _, err := schemaSync.ExecContext(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}

	if _, err := schemaSync.ExecContext(ctx, "SELECT system.create_tenant_role($1)", slug); err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() {
		_, _ = admin.Exec("DROP SCHEMA " + schema + " CASCADE")
		_, _ = schemaSync.Exec("SELECT system.drop_tenant_role($1)", slug)
	})

	if _, err := schemaSync.ExecContext(ctx, "GRANT USAGE ON SCHEMA "+schema+" TO engine_user, "+schema+"; ALTER DEFAULT PRIVILEGES IN SCHEMA "+schema+" GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO engine_user"); err != nil {
		t.Fatal(err)
	}

	store := role.NewStore(schemaSync)
	if err := store.Bootstrap(ctx, slug); err != nil {
		t.Fatal(err)
	}

	if err := store.AttachMembershipTrigger(ctx, slug); err != nil {
		t.Fatal(err)
	}

	userID, err := user.NewStore(engine).FindOrCreateInvited(ctx, "ownership@example.test")
	if err != nil {
		t.Fatal(err)
	}

	assertRows := func(q membershipQuerier, want int) {
		t.Helper()

		for _, query := range []string{
			"SELECT count(*) FROM " + schema + ".tenant_members WHERE user_id = $1",
			"SELECT count(*) FROM system.tenant_memberships WHERE user_id = $1 AND tenant_id = '" + tt.ID + "'",
		} {
			var got int
			if err := q.QueryRowContext(ctx, query, userID).Scan(&got); err != nil {
				t.Fatal(err)
			}

			if got != want {
				t.Fatalf("membership rows = %d, want %d", got, want)
			}
		}
	}

	for _, rollback := range []bool{true, false} {
		tx, err := engine.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}

		t.Cleanup(func() { _ = tx.Rollback() })
		if err := role.AddMemberTx(ctx, tx, slug, userID); err != nil {
			t.Fatal(err)
		}

		assertRows(tx, 1)
		assertRows(engine, 0)
		if rollback {
			err = tx.Rollback()
		} else {
			err = tx.Commit()
		}

		if err != nil {
			t.Fatal(err)
		}
	}

	assertRows(engine, 1)
	for _, rollback := range []bool{true, false} {
		tx, err := engine.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}

		t.Cleanup(func() { _ = tx.Rollback() })
		if err := role.RemoveMemberTx(ctx, tx, slug, userID); err != nil {
			t.Fatal(err)
		}

		assertRows(tx, 0)
		assertRows(engine, 1)
		if rollback {
			err = tx.Rollback()
		} else {
			err = tx.Commit()
		}

		if err != nil {
			t.Fatal(err)
		}
	}

	assertRows(engine, 0)
	requireMembershipOwners(t, admin)
	var bypass bool
	if err := engine.QueryRowContext(ctx, `SELECT rolbypassrls FROM pg_roles WHERE rolname = current_user`).Scan(&bypass); err != nil || bypass {
		t.Fatalf("engine BYPASSRLS = %v, err %v", bypass, err)
	}

	for _, query := range []string{"CREATE TABLE system.forbidden (id int)", "CREATE TABLE " + schema + ".forbidden (id int)"} {
		_, err := engine.ExecContext(ctx, query)
		if pgErr, ok := errors.AsType[*pgconn.PgError](err); !ok || pgErr.Code != "42501" {
			t.Fatalf("engine DDL = %v, want 42501", err)
		}
	}

	if err := role.NewStore(engine).BootstrapMembershipIndex(ctx); err == nil || !strings.Contains(err.Error(), "schema-sync pool") {
		t.Fatalf("primary bootstrap error = %v", err)
	}

	if _, err := schemaSync.ExecContext(ctx, "CREATE TABLE "+schema+".widgets (id int); GRANT SELECT, INSERT, UPDATE, DELETE ON "+schema+".widgets TO "+schema); err != nil {
		t.Fatal(err)
	}

	for _, table := range []string{"system.tenant_memberships", schema + ".tenant_members"} {
		for _, query := range []string{"SELECT * FROM " + table, "INSERT INTO " + table + " (user_id) VALUES ('" + userID + "')", "DELETE FROM " + table} {
			tx, err := engine.BeginTx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}

			if _, err := tx.ExecContext(ctx, "SELECT set_config('role', $1, true)", "tenant_"+slug); err != nil {
				t.Fatal(err)
			}

			if _, err := tx.ExecContext(ctx, "SELECT * FROM "+schema+".widgets"); err != nil {
				t.Fatalf("tenant role cannot read its module table: %v", err)
			}

			_, err = tx.ExecContext(ctx, query)
			_ = tx.Rollback()
			if pgErr, ok := errors.AsType[*pgconn.PgError](err); !ok || pgErr.Code != "42501" {
				t.Fatalf("tenant role %s = %v, want 42501", query, err)
			}
		}
	}
}
