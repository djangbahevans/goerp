package role

import (
	"errors"
	"slices"
	"testing"
	"uuid"

	"github.com/djangbahevans/goerp/internal/engine/tenantschema"
)

func TestResolveActingUser_Anonymous(t *testing.T) {
	actor, err := NewStore(nil).ResolveActingUser(t.Context(), "", "")
	if err != nil || actor.ContactID != "" || len(actor.Roles) != 0 {
		t.Fatalf("anonymous actor = %+v, error = %v", actor, err)
	}
}

func TestResolveActingUser_LiveTenantIdentity(t *testing.T) {
	s, conn, slug := openTestStore(t)
	ctx := t.Context()
	userID, contactID := uuid.New().String(), uuid.New().String()
	if err := s.AddMember(ctx, slug, userID); err != nil {
		t.Fatal(err)
	}
	if err := s.SetMemberContact(ctx, slug, userID, contactID); err != nil {
		t.Fatal(err)
	}
	if err := s.SeedBuiltinRoles(ctx, slug); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"admin", "user", "portal"} {
		id, err := s.GetRoleByName(ctx, slug, name)
		if err != nil {
			t.Fatal(err)
		}
		if err := s.AssignRole(ctx, slug, userID, id, ""); err != nil {
			t.Fatal(err)
		}
	}
	schema := tenantschema.Name(slug)
	if _, err := conn.ExecContext(ctx, `UPDATE `+schema+`.user_roles SET expires_at = NOW() - interval '1 second'
		WHERE role_id = (SELECT id FROM `+schema+`.roles WHERE name = 'portal')`); err != nil {
		t.Fatal(err)
	}
	actor, err := s.ResolveActingUser(ctx, slug, userID)
	if err != nil || actor.ContactID != contactID || !slices.Equal(actor.Roles, []string{"admin", "user"}) {
		t.Fatalf("actor = %+v, error = %v", actor, err)
	}
	if _, err := conn.ExecContext(ctx, `DELETE FROM `+schema+`.user_roles WHERE user_id = $1`, userID); err != nil {
		t.Fatal(err)
	}
	if err := s.SetMemberContact(ctx, slug, userID, ""); err != nil {
		t.Fatal(err)
	}
	actor, err = s.ResolveActingUser(ctx, slug, userID)
	if err != nil || actor.ContactID != "" || len(actor.Roles) != 0 {
		t.Fatalf("actor after revocation = %+v, error = %v", actor, err)
	}
	if _, err := conn.ExecContext(ctx, `UPDATE `+schema+`.tenant_members SET status = 'suspended' WHERE user_id = $1`, userID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ResolveActingUser(ctx, slug, userID); !errors.Is(err, ErrNotMember) {
		t.Fatalf("suspended actor error = %v, want ErrNotMember", err)
	}
	if _, err := s.ResolveActingUser(ctx, slug, uuid.New().String()); !errors.Is(err, ErrNotMember) {
		t.Fatalf("non-member error = %v, want ErrNotMember", err)
	}
	if _, err := s.ResolveActingUser(ctx, slug+"missing", userID); err == nil {
		t.Fatal("missing tenant schema must return a lookup error")
	}
}
