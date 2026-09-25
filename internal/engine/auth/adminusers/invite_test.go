package adminusers

import (
	"context"
	"encoding/json/v2"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
	"uuid"

	"github.com/djangbahevans/goerp/internal/engine/tenantschema"
)

type sentInvite struct {
	email     string
	isNewUser bool
}

type spyMailer struct {
	mu   sync.Mutex
	sent []sentInvite
}

func (m *spyMailer) SendInvite(_ context.Context, email, _, _ string, isNewUser bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sent = append(m.sent, sentInvite{email: email, isNewUser: isNewUser})
	return nil
}

func (m *spyMailer) last(t *testing.T) sentInvite {
	t.Helper()
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.sent) == 0 {
		t.Fatal("no invite email sent")
	}
	return m.sent[len(m.sent)-1]
}

func (m *spyMailer) count() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.sent)
}

func (e *env) invite(t *testing.T, ft fixtureTenant, token string, body map[string]string) (int, invitationResponse, string) {
	t.Helper()
	rec := do(t, ft, token, request{serve: e.handler.ServeInvite, method: http.MethodPost, path: "/users/invite", body: body})
	var out invitationResponse
	var errBody struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if rec.Code == http.StatusOK {
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatalf("decode invite response: %v", err)
		}
	} else {
		_ = json.Unmarshal(rec.Body.Bytes(), &errBody)
	}
	return rec.Code, out, errBody.Error.Code
}

func (e *env) invitationAction(t *testing.T, ft fixtureTenant, token, action, id string) int {
	t.Helper()
	serve := e.handler.ServeResendInvitation
	if action == "revoke" {
		serve = e.handler.ServeRevokeInvitation
	}
	rec := do(t, ft, token, request{serve: serve, method: http.MethodPost, path: "/users/invitations/" + id + "/" + action, params: map[string]string{"id": id}})
	return rec.Code
}

type invitationRow struct {
	roleName  string
	expiresAt time.Time
	revoked   bool
}

func (e *env) invitationRow(t *testing.T, ft fixtureTenant, id string) invitationRow {
	t.Helper()
	schema := tenantschema.Name(ft.slug)
	var row invitationRow
	var revokedAt *time.Time
	err := e.conn.QueryRow(fmt.Sprintf(`
		SELECT r.name, ti.expires_at, ti.revoked_at FROM %[1]s.tenant_invitations ti JOIN %[1]s.roles r ON r.id = ti.role_id WHERE ti.id = $1
	`, schema), id).Scan(&row.roleName, &row.expiresAt, &revokedAt)
	if err != nil {
		t.Fatalf("read invitation %s: %v", id, err)
	}
	row.revoked = revokedAt != nil
	return row
}

func (e *env) cleanupUserByEmail(t *testing.T, email string) {
	t.Cleanup(func() { _, _ = e.conn.Exec(`DELETE FROM system.users WHERE email = $1`, email) })
}

func (e *env) auditPerformer(t *testing.T, ft fixtureTenant, eventType, invitationID string) string {
	t.Helper()
	var performer string
	err := e.conn.QueryRow(`
		SELECT metadata->>'performed_by' FROM system.auth_audit_log
		WHERE tenant_id = $1 AND event_type = $2 AND metadata->>'invitation_id' = $3
		ORDER BY created_at DESC LIMIT 1
	`, ft.id, eventType, invitationID).Scan(&performer)
	if err != nil {
		t.Fatalf("read %s audit row: %v", eventType, err)
	}
	return performer
}

func TestServeInvite_NewEmail(t *testing.T) {
	e := newEnv(t)
	ft := e.newTenant(t)
	admin := e.member(t, ft, "admin", "Admin", "admin")
	token := e.issue(t, ft, admin)
	email := fmt.Sprintf("newperson%d@example.com", time.Now().UnixNano())
	e.cleanupUserByEmail(t, email)

	code, out, _ := e.invite(t, ft, token, map[string]string{"email": "  " + strings.ToUpper(email) + " ", "name": "New Person", "role": "user"})
	if code != http.StatusOK || out.InvitationID == "" || out.ExpiresAt.Before(time.Now().Add(6*24*time.Hour)) {
		t.Fatalf("invite = %d %+v", code, out)
	}
	invitee, err := e.users.GetByEmail(t.Context(), email)
	if err != nil {
		t.Fatalf("GetByEmail() error: %v", err)
	}
	if invitee.Status != "invited" {
		t.Errorf("invitee status = %q, want invited", invitee.Status)
	}
	if member, err := e.roles.IsMember(t.Context(), ft.slug, invitee.ID); err != nil || member {
		t.Errorf("IsMember = %v, err = %v, want no membership before acceptance", member, err)
	}
	if sent := e.mailer.last(t); sent.email != email || !sent.isNewUser {
		t.Errorf("sent = %+v, want the new-account email to %s", sent, email)
	}
	if got := e.auditPerformer(t, ft, "user.invited", out.InvitationID); got != admin {
		t.Errorf("user.invited performed_by = %q, want %s", got, admin)
	}
	detail, err := e.handler.store.get(t.Context(), ft.slug, invitee.ID)
	if err != nil || detail.Status != "invited" || detail.InvitationID == nil || *detail.InvitationID != out.InvitationID {
		t.Errorf("directory entry = %+v, err = %v", detail, err)
	}
}

func TestServeInvite_ExistingAccountFromAnotherTenant(t *testing.T) {
	e := newEnv(t)
	ft := e.newTenant(t)
	other := e.newTenant(t)
	admin := e.member(t, ft, "admin", "Admin", "admin")
	existing := e.member(t, other, "existing", "Existing Person", "user")
	if _, err := e.conn.Exec(`UPDATE system.users SET password_hash = 'x' WHERE id = $1`, existing); err != nil {
		t.Fatalf("set password: %v", err)
	}
	var email string
	if err := e.conn.QueryRow(`SELECT email FROM system.users WHERE id = $1`, existing).Scan(&email); err != nil {
		t.Fatalf("read email: %v", err)
	}
	token := e.issue(t, ft, admin)

	code, _, _ := e.invite(t, ft, token, map[string]string{"email": email, "role": "user"})
	if code != http.StatusOK {
		t.Fatalf("invite status = %d", code)
	}
	reused, err := e.users.GetByEmail(t.Context(), email)
	if err != nil || reused.ID != existing || reused.Status != "active" {
		t.Errorf("invitee = %+v, err = %v, want the existing active row %s", reused, err, existing)
	}
	if sent := e.mailer.last(t); sent.isNewUser {
		t.Error("existing account got the new-account email")
	}
}

func TestServeInvite_Rejections(t *testing.T) {
	e := newEnv(t)
	ft := e.newTenant(t)
	admin := e.member(t, ft, "admin", "Admin", "admin")
	member := e.member(t, ft, "member", "Member", "user")
	var memberEmail string
	if err := e.conn.QueryRow(`SELECT email FROM system.users WHERE id = $1`, member).Scan(&memberEmail); err != nil {
		t.Fatalf("read email: %v", err)
	}
	token := e.issue(t, ft, admin)
	fresh := fmt.Sprintf("fresh%d@example.com", time.Now().UnixNano())
	e.cleanupUserByEmail(t, fresh)

	for _, c := range []struct {
		body       map[string]string
		wantStatus int
		wantCode   string
	}{
		{map[string]string{"email": memberEmail, "role": "user"}, http.StatusConflict, "already_member"},
		{map[string]string{"email": fresh, "role": "no-such-role"}, http.StatusBadRequest, "invalid_role"},
		{map[string]string{"email": fresh}, http.StatusBadRequest, "invalid_role"},
		{map[string]string{"email": "not-an-email", "role": "user"}, http.StatusBadRequest, "invalid_email"},
		{map[string]string{"email": "Jane <jane@example.com>", "role": "user"}, http.StatusBadRequest, "invalid_email"},
	} {
		if status, _, code := e.invite(t, ft, token, c.body); status != c.wantStatus || code != c.wantCode {
			t.Errorf("invite %v = %d %q, want %d %q", c.body, status, code, c.wantStatus, c.wantCode)
		}
	}
	if n := e.mailer.count(); n != 0 {
		t.Errorf("%d emails sent for rejected invites, want 0", n)
	}
	if _, err := e.users.GetByEmail(t.Context(), fresh); err == nil {
		t.Error("a rejected invite created a user row")
	}
}

func TestServeInvite_ReinvitingUpdatesTheLiveInvitation(t *testing.T) {
	e := newEnv(t)
	ft := e.newTenant(t)
	admin := e.member(t, ft, "admin", "Admin", "admin")
	token := e.issue(t, ft, admin)
	email := fmt.Sprintf("again%d@example.com", time.Now().UnixNano())
	e.cleanupUserByEmail(t, email)

	secondAdmin := e.member(t, ft, "admin2", "Second Admin", "admin")
	_, first, _ := e.invite(t, ft, token, map[string]string{"email": email, "role": "user"})
	code, second, _ := e.invite(t, ft, e.issue(t, ft, secondAdmin), map[string]string{"email": email, "role": "admin"})
	if code != http.StatusOK || second.InvitationID != first.InvitationID {
		t.Fatalf("re-invite = %d %+v, want the same invitation %s", code, second, first.InvitationID)
	}
	if row := e.invitationRow(t, ft, first.InvitationID); row.roleName != "admin" {
		t.Errorf("invitation role = %q, want admin", row.roleName)
	}
	var invitedBy string
	if err := e.conn.QueryRow(fmt.Sprintf(`SELECT invited_by FROM %s.tenant_invitations WHERE id = $1`, tenantschema.Name(ft.slug)), first.InvitationID).Scan(&invitedBy); err != nil || invitedBy != secondAdmin {
		t.Errorf("invited_by = %q, err = %v, want the re-inviting admin %s", invitedBy, err, secondAdmin)
	}
}

func TestServeResendAndRevokeInvitation(t *testing.T) {
	e := newEnv(t)
	ft := e.newTenant(t)
	other := e.newTenant(t)
	admin := e.member(t, ft, "admin", "Admin", "admin")
	otherAdmin := e.member(t, other, "otheradmin", "Other Admin", "admin")
	token := e.issue(t, ft, admin)
	email := fmt.Sprintf("pending%d@example.com", time.Now().UnixNano())
	e.cleanupUserByEmail(t, email)
	_, inv, _ := e.invite(t, ft, token, map[string]string{"email": email, "role": "user"})
	if _, err := e.conn.Exec(fmt.Sprintf(`UPDATE %s.tenant_invitations SET expires_at = NOW() - INTERVAL '1 day' WHERE id = $1`, tenantschema.Name(ft.slug)), inv.InvitationID); err != nil {
		t.Fatalf("expire invitation: %v", err)
	}
	otherEmail := fmt.Sprintf("otherpending%d@example.com", time.Now().UnixNano())
	e.cleanupUserByEmail(t, otherEmail)
	_, otherInv, _ := e.invite(t, other, e.issue(t, other, otherAdmin), map[string]string{"email": otherEmail, "role": "user"})

	for _, id := range []string{otherInv.InvitationID, uuid.New().String(), "not-a-uuid"} {
		for _, action := range []string{"resend", "revoke"} {
			if code := e.invitationAction(t, ft, token, action, id); code != http.StatusNotFound {
				t.Errorf("%s %s: status = %d, want 404", action, id, code)
			}
		}
	}
	if e.invitationRow(t, other, otherInv.InvitationID).revoked {
		t.Error("another tenant's invitation was revoked")
	}

	sentBefore := e.mailer.count()
	if code := e.invitationAction(t, ft, token, "resend", inv.InvitationID); code != http.StatusOK {
		t.Fatalf("resend status = %d", code)
	}
	if row := e.invitationRow(t, ft, inv.InvitationID); row.expiresAt.Before(time.Now().Add(6 * 24 * time.Hour)) {
		t.Errorf("resent expires_at = %v, want about 7 days out", row.expiresAt)
	}
	if e.mailer.count() != sentBefore+1 || !e.mailer.last(t).isNewUser {
		t.Errorf("resend sent %d emails (last %+v), want one new-account email", e.mailer.count()-sentBefore, e.mailer.last(t))
	}
	if got := e.auditPerformer(t, ft, "user.invite_resent", inv.InvitationID); got != admin {
		t.Errorf("user.invite_resent performed_by = %q", got)
	}

	if code := e.invitationAction(t, ft, token, "revoke", inv.InvitationID); code != http.StatusNoContent {
		t.Fatalf("revoke status = %d", code)
	}
	if !e.invitationRow(t, ft, inv.InvitationID).revoked {
		t.Error("invitation not revoked")
	}
	if got := e.auditPerformer(t, ft, "user.invite_revoked", inv.InvitationID); got != admin {
		t.Errorf("user.invite_revoked performed_by = %q", got)
	}
	for _, action := range []string{"resend", "revoke"} {
		if code := e.invitationAction(t, ft, token, action, inv.InvitationID); code != http.StatusConflict {
			t.Errorf("%s after revoke: status = %d, want 409", action, code)
		}
	}
}

func TestInviteRoutes_RejectNonAdmin(t *testing.T) {
	e := newEnv(t)
	ft := e.newTenant(t)
	admin := e.member(t, ft, "admin", "Admin", "admin")
	nonAdmin := e.member(t, ft, "nonadmin", "Non Admin", "user")
	email := fmt.Sprintf("guarded%d@example.com", time.Now().UnixNano())
	e.cleanupUserByEmail(t, email)
	_, inv, _ := e.invite(t, ft, e.issue(t, ft, admin), map[string]string{"email": email, "role": "user"})
	token := e.issue(t, ft, nonAdmin)

	if code, _, _ := e.invite(t, ft, token, map[string]string{"email": "x" + email, "role": "admin"}); code != http.StatusForbidden {
		t.Errorf("invite as non-admin: status = %d, want 403", code)
	}
	for _, action := range []string{"resend", "revoke"} {
		if code := e.invitationAction(t, ft, token, action, inv.InvitationID); code != http.StatusForbidden {
			t.Errorf("%s as non-admin: status = %d, want 403", action, code)
		}
	}
	if e.invitationRow(t, ft, inv.InvitationID).revoked {
		t.Error("non-admin revoked the invitation")
	}
}
