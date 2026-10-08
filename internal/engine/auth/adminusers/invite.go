package adminusers

import (
	"encoding/json/v2"
	"errors"
	"net/http"
	"net/mail"
	"strings"
	"time"
	"uuid"

	"github.com/djangbahevans/goerp/internal/engine/httperr"
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

// existing_account indicates that invite acceptance adds membership without setting up a
// password. It is returned only after sending the invite to prevent account probing
// without emailing the address.
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
		httperr.Write(r.Context(), w, http.StatusBadRequest, "invalid_request", "malformed request body")
		return
	}
	email := strings.ToLower(strings.TrimSpace(body.Email))
	if addr, err := mail.ParseAddress(email); err != nil || addr.Address != email {
		httperr.Write(r.Context(), w, http.StatusBadRequest, "invalid_email", "a valid email address is required")
		return
	}
	if body.Role == "" {
		httperr.Write(r.Context(), w, http.StatusBadRequest, "invalid_role", "a role is required")
		return
	}
	if _, err := h.roles.GetRoleByName(ctx, c.tenant.Slug, body.Role); err != nil {
		if errors.Is(err, role.ErrRoleNotFound) {
			httperr.Write(r.Context(), w, http.StatusBadRequest, "invalid_role", "unknown role")
			return
		}
		writeInternalError(w, r, err, "role lookup failed")
		return
	}

	existing, err := h.users.GetByEmail(ctx, email)
	switch {
	case errors.Is(err, user.ErrUserNotFound):
	case err != nil:
		writeInternalError(w, r, err, "invitee lookup failed")
		return
	default:
		// A suspended member counts: the admin unsuspends them instead,
		// since an invitation can't lift a suspension.
		member, err := h.roles.HasMemberRow(ctx, c.tenant.Slug, existing.ID)
		if err != nil {
			writeInternalError(w, r, err, "membership check failed")
			return
		}
		if member {
			httperr.Write(r.Context(), w, http.StatusConflict, "already_member", "this person is already a member of this organisation")
			return
		}
	}

	inv, err := h.invites.Invite(ctx, c.tenant.Slug, email, body.Role, strings.TrimSpace(body.Name), &c.auth.UserID)
	if err != nil {
		writeInternalError(w, r, err, "invite failed")
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
		httperr.Write(r.Context(), w, http.StatusNotFound, "not_found", "not found")
		return nil, false
	}
	inv, err := h.invites.GetByID(r.Context(), c.tenant.Slug, id)
	if errors.Is(err, invite.ErrInvitationNotFound) {
		httperr.Write(r.Context(), w, http.StatusNotFound, "not_found", "not found")
		return nil, false
	}
	if err != nil {
		writeInternalError(w, r, err, "invitation lookup failed")
		return nil, false
	}
	return inv, true
}

func writeNotLive(w http.ResponseWriter, r *http.Request) {
	httperr.Write(r.Context(), w, http.StatusConflict, "invitation_not_live", "the invitation was already accepted or revoked")
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
		writeNotLive(w, r)
		return
	}
	if err != nil {
		writeInternalError(w, r, err, "resend failed")
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
		writeNotLive(w, r)
		return
	}
	if err != nil {
		writeInternalError(w, r, err, "revoke failed")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
