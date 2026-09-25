package adminusers

import (
	"encoding/json/v2"
	"errors"
	"net/http"
	"net/mail"
	"strings"
	"time"
	"uuid"

	"github.com/djangbahevans/goerp/internal/engine/invite"
	"github.com/djangbahevans/goerp/internal/engine/role"
	"github.com/djangbahevans/goerp/internal/engine/route"
	"github.com/djangbahevans/goerp/internal/engine/user"
)

type inviteRequest struct {
	Email string `json:"email"`
	Name  string `json:"name"`
	Role  string `json:"role"`
}

type invitationResponse struct {
	InvitationID string    `json:"invitation_id"`
	ExpiresAt    time.Time `json:"expires_at"`
}

// inviteResponse adds existing_account: the invitee already has a password,
// so accepting adds them to this tenant rather than setting up an account.
// It is reported only after the invite is sent, so it can't be used to
// probe for accounts without emailing the address.
type inviteResponse struct {
	invitationResponse
	ExistingAccount bool `json:"existing_account"`
}

// ServeInvite is POST /users/invite.
func (h *Handler) ServeInvite(w http.ResponseWriter, r *http.Request) {
	c, ok := h.authorize(w, r)
	if !ok {
		return
	}
	ctx := r.Context()

	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	var body inviteRequest
	if err := json.UnmarshalRead(r.Body, &body); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid_request", "malformed request body")
		return
	}
	email := strings.ToLower(strings.TrimSpace(body.Email))
	if addr, err := mail.ParseAddress(email); err != nil || addr.Address != email {
		writeJSONError(w, http.StatusBadRequest, "invalid_email", "a valid email address is required")
		return
	}
	if body.Role == "" {
		writeJSONError(w, http.StatusBadRequest, "invalid_role", "a role is required")
		return
	}
	if _, err := h.roles.GetRoleByName(ctx, c.tenant.Slug, body.Role); err != nil {
		if errors.Is(err, role.ErrRoleNotFound) {
			writeJSONError(w, http.StatusBadRequest, "invalid_role", "unknown role")
			return
		}
		writeInternalError(w, err, "role lookup failed")
		return
	}

	existing, err := h.users.GetByEmail(ctx, email)
	switch {
	case errors.Is(err, user.ErrUserNotFound):
	case err != nil:
		writeInternalError(w, err, "invitee lookup failed")
		return
	default:
		member, err := h.roles.IsMember(ctx, c.tenant.Slug, existing.ID)
		if err != nil {
			writeInternalError(w, err, "membership check failed")
			return
		}
		if member {
			writeJSONError(w, http.StatusConflict, "already_member", "this person is already a member of this organisation")
			return
		}
	}

	inv, err := h.invites.Invite(ctx, c.tenant.Slug, email, body.Role, strings.TrimSpace(body.Name), &c.auth.UserID)
	if err != nil {
		writeInternalError(w, err, "invite failed")
		return
	}
	writeJSON(w, http.StatusOK, inviteResponse{
		InvitationID:    inv.ID,
		ExpiresAt:       inv.ExpiresAt,
		ExistingAccount: existing != nil && existing.PasswordHash != nil,
	})
}

// invitation returns the {id} invitation when it belongs to the caller's
// tenant; any other id is a 404.
func (h *Handler) invitation(w http.ResponseWriter, r *http.Request, c caller) (*invite.Invitation, bool) {
	id := route.ParamsFromContext(r.Context())["id"]
	if _, err := uuid.Parse(id); err != nil {
		writeJSONError(w, http.StatusNotFound, "not_found", "not found")
		return nil, false
	}
	inv, err := h.invites.GetByID(r.Context(), c.tenant.Slug, id)
	if errors.Is(err, invite.ErrInvitationNotFound) {
		writeJSONError(w, http.StatusNotFound, "not_found", "not found")
		return nil, false
	}
	if err != nil {
		writeInternalError(w, err, "invitation lookup failed")
		return nil, false
	}
	return inv, true
}

func writeNotLive(w http.ResponseWriter) {
	writeJSONError(w, http.StatusConflict, "invitation_not_live", "the invitation was already accepted or revoked")
}

// ServeResendInvitation is POST /users/invitations/{id}/resend.
func (h *Handler) ServeResendInvitation(w http.ResponseWriter, r *http.Request) {
	c, ok := h.authorize(w, r)
	if !ok {
		return
	}
	inv, ok := h.invitation(w, r, c)
	if !ok {
		return
	}

	resent, err := h.invites.Resend(r.Context(), c.tenant.Slug, inv.ID, &c.auth.UserID)
	if errors.Is(err, invite.ErrInvitationNotLive) {
		writeNotLive(w)
		return
	}
	if err != nil {
		writeInternalError(w, err, "resend failed")
		return
	}
	writeJSON(w, http.StatusOK, invitationResponse{InvitationID: resent.ID, ExpiresAt: resent.ExpiresAt})
}

// ServeRevokeInvitation is POST /users/invitations/{id}/revoke.
func (h *Handler) ServeRevokeInvitation(w http.ResponseWriter, r *http.Request) {
	c, ok := h.authorize(w, r)
	if !ok {
		return
	}
	inv, ok := h.invitation(w, r, c)
	if !ok {
		return
	}

	err := h.invites.Revoke(r.Context(), c.tenant.Slug, inv.ID, &c.auth.UserID)
	if errors.Is(err, invite.ErrInvitationNotLive) {
		writeNotLive(w)
		return
	}
	if err != nil {
		writeInternalError(w, err, "revoke failed")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
