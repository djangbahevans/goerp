// Package acceptinvite implements GET /auth/accept-invite/info and POST
// /auth/accept-invite — auth-internals.md §3 "Invite acceptance",
// shell-ux.md §2.5. Class B routes (§9): the tenant comes from the invite
// link's ?tenant= (the info query string, the accept body).
//
// Accepting signs in only a brand-new invitee, whose account this call
// sets up. An invitee who already has a password gets the membership but
// no session: the invite link proves control of the mailbox, not of that
// account's password or second factor, so they sign in normally.
package acceptinvite

import (
	"context"
	"database/sql"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"net/http"
	"strconv"

	"github.com/djangbahevans/goerp/internal/engine/auth/authtoken"
	"github.com/djangbahevans/goerp/internal/engine/auth/loginsession"
	"github.com/djangbahevans/goerp/internal/engine/auth/password"
	"github.com/djangbahevans/goerp/internal/engine/invite"
	"github.com/djangbahevans/goerp/internal/engine/tenant"
	"github.com/djangbahevans/goerp/internal/engine/user"
)

const maxBodyBytes = 64 * 1024

type Handlers struct {
	tenants  *tenant.Store
	invites  *invite.Store
	users    *user.Store
	policies *password.PolicyStore
	hasher   *password.Hasher
	issuer   *authtoken.Issuer
}

func NewHandlers(tenants *tenant.Store, invites *invite.Store, users *user.Store, policies *password.PolicyStore, hasher *password.Hasher, issuer *authtoken.Issuer) *Handlers {
	return &Handlers{tenants: tenants, invites: invites, users: users, policies: policies, hasher: hasher, issuer: issuer}
}

func writeJSON(w http.ResponseWriter, v any) {
	_ = json.MarshalWrite(w, v, jsontext.EscapeForHTML(true), jsontext.EscapeForJS(true))
}

func writeJSONError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	writeJSON(w, map[string]any{
		"error": map[string]string{"code": code, "message": message},
	})
}

func writeInvalidInvite(w http.ResponseWriter) {
	writeJSONError(w, http.StatusNotFound, "invalid_invite", "invite link is invalid or has expired")
}

func writeInternal(w http.ResponseWriter) {
	writeJSONError(w, http.StatusInternalServerError, "internal_error", "invite acceptance failed")
}

// lookup resolves the tenant and live invitation behind a link, and the
// invitee's account. errNotFound covers every way the link can be dead.
// GetBySlug runs first: the invite store interpolates the slug into a
// schema name, safe only for a slug read back from a real row.
func (h *Handlers) lookup(ctx context.Context, tenantSlug, rawToken string) (*tenant.Tenant, *invite.Invitation, *user.User, error) {
	t, err := h.tenants.GetBySlug(ctx, tenantSlug)
	if err != nil {
		if errors.Is(err, tenant.ErrTenantNotFound) {
			return nil, nil, nil, errNotFound
		}
		return nil, nil, nil, err
	}
	inv, err := h.invites.GetLiveByToken(ctx, t.Slug, rawToken)
	if err != nil {
		if errors.Is(err, invite.ErrInvitationNotLive) {
			return nil, nil, nil, errNotFound
		}
		return nil, nil, nil, err
	}
	u, err := h.users.GetByEmail(ctx, inv.Email)
	if err != nil {
		// The invitee's row was deleted after the invite was sent.
		if errors.Is(err, user.ErrUserNotFound) {
			return nil, nil, nil, errNotFound
		}
		return nil, nil, nil, err
	}
	return t, inv, u, nil
}

var errNotFound = errors.New("invite not found")

// Info serves GET /auth/accept-invite/info?token=&tenant= — the accept
// page's prefill. password_required tells the page which path to show.
func (h *Handlers) Info(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	q := r.URL.Query()

	t, _, u, err := h.lookup(ctx, q.Get("tenant"), q.Get("token"))
	if err != nil {
		if errors.Is(err, errNotFound) {
			writeInvalidInvite(w)
			return
		}
		writeInternal(w)
		return
	}

	var name *string
	if p, err := h.users.GetProfile(ctx, u.ID); err == nil {
		name = &p.Name
	} else if !errors.Is(err, user.ErrProfileNotFound) {
		writeInternal(w)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	writeJSON(w, map[string]any{
		"tenant_name":       t.Name,
		"email":             u.Email,
		"name":              name,
		"password_required": u.PasswordHash == nil,
	})
}

type acceptRequest struct {
	Token    string `json:"token"`
	Tenant   string `json:"tenant"`
	Password string `json:"password"`
	DeviceID string `json:"device_id"`
}

// Accept serves POST /auth/accept-invite.
func (h *Handlers) Accept(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	var req acceptRequest
	if err := json.UnmarshalRead(r.Body, &req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid_request", "malformed request body")
		return
	}

	ctx := r.Context()
	t, inv, u, err := h.lookup(ctx, req.Tenant, req.Token)
	if err != nil {
		if errors.Is(err, errNotFound) {
			writeInvalidInvite(w)
			return
		}
		writeInternal(w)
		return
	}

	newUser := u.PasswordHash == nil
	var activate func(*sql.Tx) error
	if newUser {
		policy, policyVersion, err := h.policies.Effective(ctx, t.ID)
		if err != nil {
			writeInternal(w)
			return
		}
		if err := policy.Validate(req.Password, u.Email); err != nil {
			writeJSONError(w, http.StatusUnprocessableEntity, "auth.password_too_weak", err.Error())
			return
		}
		slot, err := h.hasher.Acquire(ctx)
		if err != nil {
			w.Header().Set("Retry-After", strconv.Itoa(password.OverloadRetryAfterSeconds))
			writeJSONError(w, http.StatusServiceUnavailable, "overloaded", "too many password operations in progress, retry shortly")
			return
		}
		hash, err := slot.Hash(req.Password)
		slot.Release()
		if err != nil {
			writeInternal(w)
			return
		}
		activate = func(tx *sql.Tx) error {
			return h.users.ActivateWithPasswordTx(ctx, tx, u.ID, hash, t.ID, policyVersion)
		}
	}

	if err := h.invites.Accept(ctx, t.Slug, inv.ID, u.ID, activate); err != nil {
		switch {
		case errors.Is(err, invite.ErrInvitationNotLive):
			writeInvalidInvite(w)
		case errors.Is(err, user.ErrNotActivatable):
			// A concurrent accept (or reset) set the password first, or the
			// account's status changed; this request's password was never
			// applied.
			writeJSONError(w, http.StatusConflict, "invite_conflict", "this account was set up by another request; sign in instead")
		default:
			writeInternal(w)
		}
		return
	}

	if !newUser {
		w.Header().Set("Content-Type", "application/json")
		writeJSON(w, map[string]any{"login_required": true})
		return
	}

	nonBrowser := loginsession.IsNonBrowser(r)
	deviceID, deviceIDIsFresh := loginsession.ResolveDeviceID(r, req.DeviceID, nonBrowser)
	tokens, err := h.issuer.Issue(ctx, authtoken.LoginParams{
		UserID:     u.ID,
		TenantSlug: t.Slug,
		DeviceID:   deviceID,
		UserAgent:  r.UserAgent(),
		IPAddress:  loginsession.ClientIP(r),
		Persistent: nonBrowser,
	})
	if err != nil {
		// The account is set up and the membership granted; only the
		// automatic sign-in failed, so the user signs in normally.
		w.Header().Set("Content-Type", "application/json")
		writeJSON(w, map[string]any{"login_required": true})
		return
	}
	loginsession.WriteResponse(w, tokens, deviceID, deviceIDIsFresh, nonBrowser, false)
}
