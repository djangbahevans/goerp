package domain

import (
	"context"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
)

func TestPermissionRLSMembership(t *testing.T) {
	conn, err := pgx.Connect(t.Context(), localPostgresDSN)
	if err != nil {
		t.Skipf("dev Postgres unreachable: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close(context.Background()) })

	for _, tc := range []struct {
		name        string
		permissions []string
		want        bool
	}{
		{"unset", nil, false},
		{"exact", []string{"contacts:personal:read"}, true},
		{"substring", []string{"contacts:personal:read_more"}, false},
		{"different permission", []string{"contacts:personal:write"}, false},
		{"multiple", []string{"contacts:identifier:read", "contacts:personal:read"}, true},
		{"revoked", nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tx, err := conn.Begin(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = tx.Rollback(t.Context()) }()
			if _, err := tx.Exec(t.Context(), `SELECT set_config('app.current_user_permissions',$1,true)`, strings.Join(tc.permissions, ",")); err != nil {
				t.Fatal(err)
			}

			var got bool
			query := "SELECT " + compileSrc(t, "user_has_permission('contacts:personal:read')")
			if err := tx.QueryRow(t.Context(), query).Scan(&got); err != nil {
				t.Fatal(err)
			}
			if got != tc.want {
				t.Fatalf("permission membership=%v, want %v", got, tc.want)
			}
		})
	}
}
