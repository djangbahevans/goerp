package passwordreset

import (
	"context"
	"encoding/json/v2"
	"errors"
	"net/http"

	"github.com/rs/zerolog/log"

	"github.com/djangbahevans/goerp/internal/engine/auth/authtoken"
	"github.com/djangbahevans/goerp/internal/engine/auth/loginsession"
	"github.com/djangbahevans/goerp/internal/engine/auth/password"
	"github.com/djangbahevans/goerp/internal/engine/auth/sessionrevoke"
	"github.com/djangbahevans/goerp/internal/engine/authaudit"
	"github.com/djangbahevans/goerp/internal/engine/mfa"
	"github.com/djangbahevans/goerp/internal/engine/role"
	"github.com/djangbahevans/goerp/internal/engine/tenant"
	"github.com/djangbahevans/goerp/internal/engine/user"
)

// revokeReason is the sessions.revoke_reason value auth-internals.md §4
// defines for a password change.
const revokeReason = "password_change"

type ConfirmHandler struct {
	users    *user.Store
	tenants  *tenant.Store
	roles    *role.Store
	mfa      *mfa.Store
	sessions *sessionrevoke.Revoker
	issuer   *authtoken.Issuer
	mailer   Mailer
	audit    AuditRecorder
}

func NewConfirmHandler(users *user.Store, tenants *tenant.Store, roles *role.Store, mfaStore *mfa.Store, sessions *sessionrevoke.Revoker, issuer *authtoken.Issuer, mailer Mailer, audit AuditRecorder) *ConfirmHandler {
	return &ConfirmHandler{users: users, tenants: tenants, roles: roles, mfa: mfaStore, sessions: sessions, issuer: issuer, mailer: mailer, audit: audit}
}

type confirmRequest struct {
	Token       string `json:"token"`
	NewPassword string `json:"new_password"`
	Tenant      string `json:"tenant"`
	DeviceID    string `json:"device_id"`
}

func (h *ConfirmHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	var req confirmRequest
	if err := json.UnmarshalRead(r.Body, &req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid_request", "malformed request body")
		return
	}

	ctx := r.Context()
	tokenHash := hashToken(req.Token)

	u, err := h.users.GetByPasswordResetToken(ctx, tokenHash)
	if err != nil {
		if errors.Is(err, user.ErrResetTokenInvalid) {
			writeInvalidToken(w)
			return
		}
		writeJSONError(w, http.StatusInternalServerError, "internal_error", "password reset failed")
		return
	}

	if err := password.Validate(req.NewPassword, u.Email); err != nil {
		writeJSONError(w, http.StatusUnprocessableEntity, "auth.password_too_weak", err.Error())
		return
	}

	hash, err := password.Hash(req.NewPassword)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "internal_error", "password reset failed")
		return
	}
	// Revoked before the token is consumed, so a revocation failure leaves
	// the link usable for a retry.
	if err := h.sessions.RevokeAllForUser(ctx, u.ID, revokeReason); err != nil {
		writeJSONError(w, http.StatusInternalServerError, "internal_error", "password reset failed")
		return
	}
	// The token can be consumed by a concurrent confirm between the
	// lookup above and here; the conditional update is the real check.
	if _, err := h.users.ConsumePasswordResetToken(ctx, tokenHash, hash); err != nil {
		if errors.Is(err, user.ErrResetTokenInvalid) {
			writeInvalidToken(w)
			return
		}
		writeJSONError(w, http.StatusInternalServerError, "internal_error", "password reset failed")
		return
	}
	// Catches a session a login with the old password created in between.
	if err := h.sessions.RevokeAllForUser(ctx, u.ID, revokeReason); err != nil {
		log.Error().Err(err).Str("user_id", u.ID).Msg("passwordreset: post-reset session revocation failed")
	}

	// The password is already changed, so a lookup failure here degrades
	// to login_required rather than failing the request.
	t, sessionAllowed, err := h.sessionTenant(ctx, u, req.Tenant)
	if err != nil {
		log.Error().Err(err).Str("user_id", u.ID).Msg("passwordreset: session eligibility check failed")
		sessionAllowed = false
	}

	auditRow := authaudit.Row{
		EventType: "password.reset_completed",
		UserID:    u.ID,
		IPAddress: loginsession.ClientIP(r),
		UserAgent: r.UserAgent(),
		Success:   true,
	}
	if t != nil {
		auditRow.TenantID = t.ID
	}
	recordAudit(ctx, h.audit, auditRow)

	if h.mailer != nil {
		sendDetached(ctx, "reset confirmation", func(ctx context.Context) error {
			return h.mailer.SendPasswordResetConfirmed(ctx, u.Email)
		})
	}

	if !sessionAllowed {
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
		writeJSONError(w, http.StatusInternalServerError, "internal_error", "password reset failed")
		return
	}
	if err := h.users.ResetLoginState(ctx, u.ID, loginsession.ClientIP(r)); err != nil {
		writeJSONError(w, http.StatusInternalServerError, "internal_error", "password reset failed")
		return
	}

	loginsession.WriteResponse(w, tokens, deviceID, deviceIDIsFresh, nonBrowser)
}

// sessionTenant reports whether confirm may sign the user straight in:
// only an active member of the tenant with no MFA enrolled. A reset link
// proves control of the mailbox, not of a second factor, and a reset
// never changes status (auth-internals.md §2), so everyone else is sent
// to the normal login flow. The tenant is returned whenever it resolves.
func (h *ConfirmHandler) sessionTenant(ctx context.Context, u *user.User, tenantSlug string) (*tenant.Tenant, bool, error) {
	if tenantSlug == "" {
		return nil, false, nil
	}
	t, err := h.tenants.GetBySlug(ctx, tenantSlug)
	if err != nil {
		if errors.Is(err, tenant.ErrTenantNotFound) {
			return nil, false, nil
		}
		return nil, false, err
	}
	if u.Status != user.StatusActive {
		return t, false, nil
	}
	isMember, err := h.roles.IsMember(ctx, t.Slug, u.ID)
	if err != nil || !isMember {
		return t, false, err
	}
	factors, err := h.mfa.ListActiveByUser(ctx, u.ID)
	if err != nil {
		return t, false, err
	}
	return t, len(factors) == 0, nil
}

func writeInvalidToken(w http.ResponseWriter) {
	writeJSONError(w, http.StatusNotFound, "invalid_token", "reset link is invalid or has expired")
}
