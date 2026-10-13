package tenantimport

import (
	"encoding/json/v2"
	"fmt"
	"testing"
	"time"
	"uuid"

	"github.com/djangbahevans/goerp/internal/engine/invite"
	"github.com/djangbahevans/goerp/internal/engine/role"
	"github.com/djangbahevans/goerp/internal/engine/tenant/transfer"
	"github.com/djangbahevans/goerp/internal/engine/tenantschema"
	"github.com/djangbahevans/goerp/internal/engine/user"
)

func TestMapAccounts_InvitedMatchAndHistoricalReference(t *testing.T) {
	f := newImportTestFixture(t)
	ctx := t.Context()
	users := user.NewStore(f.conn)
	existing, err := users.FindOrCreateInvited(ctx, "existing@transfer.test")
	if err != nil {
		t.Fatal(err)
	}
	if err := users.EnsureProfile(ctx, existing, "Target Name"); err != nil {
		t.Fatal(err)
	}
	job := testJob(time.Now().UnixNano(), Args{NewSlug: "accounttarget"})
	target, err := f.worker.resolveTenant(ctx, job)
	if err != nil {
		t.Fatal(err)
	}
	oldMember, oldHistorian := uuid.NewV7().String(), uuid.NewV7().String()
	data, err := json.Marshal([]transfer.Account{
		{ID: oldMember, Email: "EXISTING@transfer.test", Name: "Source Name"},
		{ID: oldHistorian, Email: "historical@transfer.test", Name: "Historical Name"},
	})
	if err != nil {
		t.Fatal(err)
	}
	mapping, err := f.worker.mapAccounts(ctx, job, target, data)
	if err != nil {
		t.Fatal(err)
	}
	if mapping.IDs[oldMember] != existing || mapping.Matched != 1 || mapping.Created != 1 {
		t.Fatalf("mapping %+v", mapping)
	}
	profile, err := users.GetProfile(ctx, existing)
	if err != nil || profile.Name != "Target Name" {
		t.Fatalf("matched profile %+v, %v", profile, err)
	}
	restored, err := f.worker.mapAccounts(ctx, job, target, data)
	if err != nil || restored.Created != 1 || restored.IDs[oldHistorian] != mapping.IDs[oldHistorian] {
		t.Fatalf("retry mapping %+v, %v", restored, err)
	}

	schema := tenantschema.Name(target.Slug)
	execSQL(t, f.conn, "CREATE SCHEMA "+schema)
	if err := role.NewStore(f.conn).Bootstrap(ctx, target.Slug); err != nil {
		t.Fatal(err)
	}
	if err := role.NewStore(f.conn).SeedBuiltinRoles(ctx, target.Slug); err != nil {
		t.Fatal(err)
	}
	if err := invite.NewStore(f.conn, nil, nil, nil, nil).Bootstrap(ctx, target.Slug); err != nil {
		t.Fatal(err)
	}
	roleID, err := role.NewStore(f.conn).GetRoleByName(ctx, target.Slug, "user")
	if err != nil {
		t.Fatal(err)
	}
	execSQL(t, f.conn, fmt.Sprintf("INSERT INTO %s.tenant_members (user_id) VALUES ($1)", schema), existing)
	execSQL(t, f.conn, fmt.Sprintf("INSERT INTO %s.user_roles (user_id, role_id) VALUES ($1, $2)", schema), existing, roleID)
	mailer := &importMailer{}
	f.worker.Invites = invite.NewStore(f.conn, users, role.NewStore(f.conn), nil, mailer)
	if err := f.worker.inviteRestoredMembers(ctx, job, target); err != nil {
		t.Fatal(err)
	}
	if len(mailer.emails) != 1 || mailer.emails[0] != "existing@transfer.test" {
		t.Fatalf("invitations %v", mailer.emails)
	}
}

func TestRewriteIdentities_RejectsMissingAccountMapping(t *testing.T) {
	oldID := uuid.NewV7().String()
	row := map[string]any{"created_by": oldID}
	if err := rewriteIdentities(row, []transfer.Column{{Name: "created_by", User: true}}, map[string]string{}, ""); err == nil {
		t.Fatal("unmapped account accepted")
	}
}
