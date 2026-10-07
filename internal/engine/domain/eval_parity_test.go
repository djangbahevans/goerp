package domain

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
)

// localPostgresDSN points directly at the compose.dev.yml Postgres.
const localPostgresDSN = "postgres://goerp:dev@localhost:15432/goerp"

// TestEval_AgreesWithTheRLSCompilation runs the RLS form of each condition in
// Postgres against a one-row relation and requires the in-memory verdict to
// match, so the two enforcement points of a policy cannot drift apart.
func TestEval_AgreesWithTheRLSCompilation(t *testing.T) {
	ctx := t.Context()
	conn, err := pgx.Connect(ctx, localPostgresDSN)
	if err != nil {
		t.Skipf("dev Postgres unreachable at %s (start compose.dev.yml): %v", localPostgresDSN, err)
	}
	t.Cleanup(func() { _ = conn.Close(context.WithoutCancel(ctx)) })

	const relation = `(SELECT 5::int AS qty, 9.5::float8 AS price, true AS paid, 'draft'::text AS state,
		NULL::text AS note, '` + userA + `'::uuid AS owner_id, NULL::uuid AS manager_id) AS r`
	record := map[string]any{
		"qty": int64(5), "price": 9.5, "paid": true, "state": "draft", "note": nil,
		"owner_id": userA, "manager_id": nil,
	}

	users := []struct {
		name  string
		env   Env
		roles string
	}{
		{"owner with a role", Env{UserID: userA, ContactID: userB, Roles: []string{"sales_manager"}}, "sales_manager"},
		{"other user without roles", Env{UserID: userB, ContactID: userA}, ""},
		{"no contact", Env{UserID: userA}, ""},
	}
	conditions := []string{
		"record.state = 'draft'",
		"record.state != 'draft'",
		"record.qty > 4 AND record.qty <= 5",
		"record.qty < 5 OR record.qty >= 6",
		"record.qty = 5.0",
		"record.price >= 9.5",
		"record.paid = true",
		"NOT record.paid",
		"record.owner_id = current_user.id",
		"record.owner_id = current_user.contact_id",
		"record.owner_id = current_user.contact_id OR user_has_role('sales_manager')",
		"user_has_role('sales_manager')",
		"record.note IS NULL",
		"record.note IS NOT NULL",
		"record.manager_id IS NULL",
		"record.state IN ('draft', 'confirmed')",
		"record.state IN ('cancelled')",
		"record.qty IN (1, 5)",
		"record.note = 'x'",
		"record.note != 'x'",
		"NOT (record.note = 'x')",
		"record.note = 'x' OR record.state = 'draft'",
		"record.note = 'x' AND record.state = 'draft'",
		"record.note = 'x' OR record.state = 'other'",
		"record.note IN ('x')",
		"record.state IN ('x', null)",
		"record.state IN ('draft', null)",
		"record.manager_id = current_user.contact_id",
		"current_user.contact_id IS NULL",
	}

	for _, u := range users {
		if _, err := conn.Exec(ctx, `SELECT set_config('app.current_user_id', $1, false),
			set_config('app.current_user_contact_id', $2, false), set_config('app.current_user_roles', $3, false)`,
			u.env.UserID, u.env.ContactID, u.roles); err != nil {
			t.Fatalf("set session variables: %v", err)
		}
		for _, src := range conditions {
			t.Run(u.name+"/"+src, func(t *testing.T) {
				expr, err := Parse(src)
				if err != nil {
					t.Fatalf("Parse: %v", err)
				}
				rls, err := CompileToRLS(expr)
				if err != nil {
					t.Fatalf("CompileToRLS: %v", err)
				}

				var inPostgres bool
				query := fmt.Sprintf("SELECT COALESCE(%s, false) FROM %s", rls, relation)
				if err := conn.QueryRow(ctx, query).Scan(&inPostgres); err != nil {
					t.Fatalf("run %s: %v", strings.TrimSpace(query), err)
				}

				env := u.env
				env.Record = record
				inMemory, err := Eval(expr, env)
				if err != nil {
					t.Fatalf("Eval: %v", err)
				}
				if inMemory != inPostgres {
					t.Errorf("in memory = %v, Postgres = %v", inMemory, inPostgres)
				}
			})
		}
	}
}
