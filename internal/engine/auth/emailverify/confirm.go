package emailverify

import (
	"context"
	"encoding/json/v2"
	"errors"
	"net/http"

	"github.com/rs/zerolog/log"

	"github.com/djangbahevans/goerp/internal/engine/auth/authtoken"
	"github.com/djangbahevans/goerp/internal/engine/auth/loginsession"
	"github.com/djangbahevans/goerp/internal/engine/mfa"
	"github.com/djangbahevans/goerp/internal/engine/role"
	"github.com/djangbahevans/goerp/internal/engine/tenant"
	"github.com/djangbahevans/goerp/internal/engine/user"
)

type ConfirmHandler struct {
	users   *user.Store
	tenants *tenant.Store
	roles   *role.Store
	mfa     *mfa.Store
	issuer  *authtoken.Issuer
}

func NewConfirmHandler(users *user.Store, tenants *tenant.Store, roles *role.Store, mfaStore *mfa.Store, issuer *authtoken.Issuer) *ConfirmHandler {
	return &ConfirmHandler{users: users, tenants: tenants, roles: roles, mfa: mfaStore, issuer: issuer}
}

type confirmRequest struct {
	Token    string `json:"token"`
	Tenant   string `json:"tenant"`
	DeviceID string `json:"device_id"`
}

func (h *ConfirmHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	var req confirmRequest
	if err := json.UnmarshalRead(r.Body, &req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid_request", "malformed request body")
		return
	}

	ctx := r.Context()
	u, err := h.users.ConsumeEmailVerifyToken(ctx, hashToken(req.Token))
	if err != nil {
		if errors.Is(err, user.ErrVerifyTokenInvalid) {
			writeJSONError(w, http.StatusNotFound, "invalid_token", "verification link is invalid or has expired")
			return
		}
		writeJSONError(w, http.StatusInternalServerError, "internal_error", "email verification failed")
		return
	}

	// The email is verified by now, so a failed membership or MFA lookup
	// degrades to login_required rather than failing the request.
	t, ok := h.signInTenant(ctx, u, req.Tenant)
	if !ok {
		writeJSON(w, http.StatusOK, map[string]any{"login_required": true})
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
		writeJSONError(w, http.StatusInternalServerError, "internal_error", "email verification failed")
		return
	}
	if err := h.users.ResetLoginState(ctx, u.ID, loginsession.ClientIP(r)); err != nil {
		log.Error().Err(err).Str("user_id", u.ID).Msg("emailverify: updating login state failed")
	}

	loginsession.WriteResponse(w, tokens, deviceID, deviceIDIsFresh, nonBrowser, false)
}

// signInTenant returns the named tenant when the confirm may sign u
// straight into it: u is active, a member, and has no MFA enrolled. The
// link proves control of the mailbox, not of a second factor. GetBySlug
// runs before IsMember because IsMember interpolates the slug into a
// schema name, which is safe only for a slug read back from a real row.
func (h *ConfirmHandler) signInTenant(ctx context.Context, u *user.User, tenantSlug string) (*tenant.Tenant, bool) {
	if u.Status != user.StatusActive || tenantSlug == "" {
		return nil, false
	}
	t, err := h.tenants.GetBySlug(ctx, tenantSlug)
	if err != nil {
		if !errors.Is(err, tenant.ErrTenantNotFound) {
			log.Error().Err(err).Str("user_id", u.ID).Msg("emailverify: tenant lookup failed")
		}
		return nil, false
	}
	isMember, err := h.roles.IsMember(ctx, t.Slug, u.ID)
	if err != nil {
		log.Error().Err(err).Str("user_id", u.ID).Msg("emailverify: membership check failed")
		return nil, false
	}
	if !isMember {
		return nil, false
	}
	factors, err := h.mfa.ListActiveByUser(ctx, u.ID)
	if err != nil {
		log.Error().Err(err).Str("user_id", u.ID).Msg("emailverify: mfa enrollment check failed")
		return nil, false
	}
	return t, len(factors) == 0
}
