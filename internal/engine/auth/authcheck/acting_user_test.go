package authcheck

import (
	"slices"
	"testing"
	"uuid"

	"github.com/djangbahevans/goerp/internal/engine/tenantschema"
)

func TestAuthenticate_LiveActingUser(t *testing.T) {
	for _, method := range []string{"jwt", "api_key"} {
		t.Run(method, func(t *testing.T) {
			f := newFixture(t)
			ctx := t.Context()
			token := f.issueToken(t, "")
			if method == "api_key" {
				var err error
				token, _, err = f.apiKeys.IssueKey(ctx, f.tenantID, &f.userID, "Acting User", nil, nil, nil, nil)
				if err != nil {
					t.Fatal(err)
				}
			}
			schema := tenantschema.Name(f.tenantSlug)
			contactID := uuid.New().String()
			if _, err := f.conn.ExecContext(ctx, `UPDATE `+schema+`.tenant_members SET contact_id = $1 WHERE user_id = $2`, contactID, f.userID); err != nil {
				t.Fatal(err)
			}
			var roleID string
			if err := f.conn.QueryRowContext(ctx, `INSERT INTO `+schema+`.roles (name) VALUES ('sales_manager') RETURNING id`).Scan(&roleID); err != nil {
				t.Fatal(err)
			}
			if _, err := f.conn.ExecContext(ctx, `INSERT INTO `+schema+`.user_roles (user_id, role_id) VALUES ($1, $2)`, f.userID, roleID); err != nil {
				t.Fatal(err)
			}
			check := func(wantContact string, wantRoles []string) {
				t.Helper()
				auth, err := f.checker.Authenticate(ctx, token, f.tenantID, f.tenantSlug, "127.0.0.1", f.permissions, nil)
				if err != nil {
					t.Fatal(err)
				}
				if auth.ContactID != wantContact || !slices.Equal(auth.RolesLive, wantRoles) {
					t.Fatalf("contact = %q, live roles = %v; want %q, %v", auth.ContactID, auth.RolesLive, wantContact, wantRoles)
				}
				if method == "jwt" && !slices.Equal(auth.Roles, []string{"admin"}) {
					t.Fatalf("JWT snapshot changed: %v", auth.Roles)
				}
			}
			check(contactID, []string{"admin", "sales_manager"})
			if _, err := f.conn.ExecContext(ctx, `UPDATE `+schema+`.user_roles SET expires_at = NOW() - interval '1 second' WHERE role_id = $1`, roleID); err != nil {
				t.Fatal(err)
			}
			check(contactID, []string{"admin"})
			if _, err := f.conn.ExecContext(ctx, `UPDATE `+schema+`.tenant_members SET contact_id = NULL WHERE user_id = $1`, f.userID); err != nil {
				t.Fatal(err)
			}
			check("", []string{"admin"})
		})
	}
}

func TestAuthenticate_ServiceKeyHasNoActingUser(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	token, _, err := f.apiKeys.IssueKey(ctx, f.tenantID, nil, "Service", nil, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	auth, err := f.checker.Authenticate(ctx, token, f.tenantID, f.tenantSlug, "127.0.0.1", f.permissions, nil)
	if err != nil {
		t.Fatal(err)
	}
	if auth.UserID != "" || auth.ContactID != "" || len(auth.RolesLive) != 0 {
		t.Fatalf("service key actor = %+v", auth)
	}
}
