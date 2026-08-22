// Package mfareverify implements POST /auth/mfa/reverify — auth-internals.md
// §8 "Step-up re-verification": refreshing an already-`Authenticated`
// session's MFA assurance without a full re-login, when its assurance has
// simply aged past `mfa_max_assurance_age` rather than never having
// existed.
//
// Unlike /auth/mfa/verify (goerp#304), this route requires a currently-
// valid access token, not an mfa_token — auth-internals.md §9 "Route
// classes" places it in Class A (the standard Host-header tenant
// resolution, JWT token validation). The generic middleware pipeline that
// would normally run tenant resolution and JWT validation ahead of every
// Class A route doesn't exist yet (goerp#91, still blocked); this handler
// calls the same underlying primitives directly instead —
// tenantresolve.Resolver.ResolveByHost for step 5, authcheck.Checker for
// step 7 — the same "call the primitive directly, skip the not-yet-built
// generic middleware" pattern loginflow and mfaverify already use for
// authtoken.Issuer and authcheck's sibling pieces. goerp#91/#224 will
// later lift this same logic into the automatic per-request pipeline;
// nothing here needs to be unwound when that happens.
package mfareverify

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/auth/authcheck"
	"github.com/djangbahevans/goerp/internal/engine/auth/authtoken"
	"github.com/djangbahevans/goerp/internal/engine/auth/loginsession"
	"github.com/djangbahevans/goerp/internal/engine/auth/mfaverify"
	"github.com/djangbahevans/goerp/internal/engine/auth/session"
	"github.com/djangbahevans/goerp/internal/engine/mfa/lockout"
	"github.com/djangbahevans/goerp/internal/engine/mfa/recoverycode"
	"github.com/djangbahevans/goerp/internal/engine/mfa/totp"
	tenantresolve "github.com/djangbahevans/goerp/internal/engine/tenant/resolve"
)

// maxBodyBytes bounds the request body before JSON parsing — no shared
// config field or middleware covers builtin routes yet, same reasoning
// loginflow/mfaverify's own caps use.
const maxBodyBytes = 64 * 1024

type Handler struct {
	tenants  *tenantresolve.Resolver
	auth     *authcheck.Checker
	sessions *session.Store
	issuer   *authtoken.Issuer
	totp     *totp.Service
	recovery *recoverycode.Service
	lockout  *lockout.Counter
}

func NewHandler(tenants *tenantresolve.Resolver, auth *authcheck.Checker, sessions *session.Store, issuer *authtoken.Issuer, totpService *totp.Service, recoveryService *recoverycode.Service, lockoutCounter *lockout.Counter) *Handler {
	return &Handler{
		tenants:  tenants,
		auth:     auth,
		sessions: sessions,
		issuer:   issuer,
		totp:     totpService,
		recovery: recoveryService,
		lockout:  lockoutCounter,
	}
}

type reverifyRequest struct {
	Type string `json:"type"`
	Code string `json:"code"`
}

func writeJSONError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"error": map[string]string{"code": code, "message": message},
	})
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// Step 5 (Class A): Host-header tenant resolution.
	tenantCtx, err := h.tenants.ResolveByHost(ctx, r.Host)
	if err != nil {
		switch {
		case errors.Is(err, tenantresolve.ErrTenantNotFound):
			writeJSONError(w, http.StatusNotFound, "not_found", "not found")
		case errors.Is(err, tenantresolve.ErrTenantSuspended):
			writeJSONError(w, http.StatusForbidden, "tenant_suspended", "tenant suspended")
		default:
			writeJSONError(w, http.StatusInternalServerError, "internal_error", "reverification failed")
		}
		return
	}

	// Step 7 (Class A, JWT branch): requires a currently-valid access
	// token — auth-internals.md §8's own distinction from /auth/mfa/verify.
	rawToken := authcheck.ExtractToken(r)
	if rawToken == "" {
		writeJSONError(w, http.StatusUnauthorized, "unauthenticated", "a valid access token is required")
		return
	}
	authCtx, err := h.auth.Authenticate(ctx, rawToken, tenantCtx.TenantID, tenantCtx.Slug, loginsession.ClientIP(r), nil, nil)
	if err != nil || !authCtx.IsAuthenticated {
		writeJSONError(w, http.StatusUnauthorized, "unauthenticated", "a valid access token is required")
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	var req reverifyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid_request", "malformed request body")
		return
	}

	// Step 5 of §8's re-verification flow: lockout check, before spending
	// effort verifying the code — same counter and threshold
	// /auth/mfa/verify uses, scoped the same way (user_id, tenant_id).
	locked, err := h.lockout.Locked(ctx, authCtx.UserID, authCtx.TenantID)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "internal_error", "reverification failed")
		return
	}
	if locked {
		writeJSONError(w, http.StatusLocked, "mfa_locked", "too many failed MFA attempts; try again later")
		return
	}

	// Step 1: verify the submitted code against any of the user's
	// enrolled factors — not necessarily the one originally used for this
	// session, per auth-internals.md §8's own wording.
	valid, credentialID, err := mfaverify.VerifyCode(ctx, h.totp, h.recovery, req.Type, authCtx.UserID, req.Code)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "internal_error", "reverification failed")
		return
	}
	if !valid {
		if err := h.lockout.RecordFailure(ctx, authCtx.UserID, authCtx.TenantID); err != nil {
			writeJSONError(w, http.StatusInternalServerError, "internal_error", "reverification failed")
			return
		}
		writeJSONError(w, http.StatusUnauthorized, "invalid_mfa_code", "invalid MFA code")
		return
	}

	// Step 2: update the session's MFA assurance columns, then issue a
	// fresh access token carrying the updated amr/mfa_verified_at claims
	// — same session, refresh token unchanged.
	now := time.Now()
	if err := h.sessions.UpdateMFAAssurance(ctx, authCtx.SessionID, req.Type, now, credentialID); err != nil {
		writeJSONError(w, http.StatusInternalServerError, "internal_error", "reverification failed")
		return
	}
	accessToken, expiresIn, err := h.issuer.ReissueAccessToken(authCtx.SessionID, authCtx.TenantID, authCtx.UserID, authCtx.RolesLive, req.Type, &now)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "internal_error", "reverification failed")
		return
	}

	if err := h.lockout.Reset(ctx, authCtx.UserID, authCtx.TenantID); err != nil {
		writeJSONError(w, http.StatusInternalServerError, "internal_error", "reverification failed")
		return
	}

	writeAccessToken(w, r, accessToken, expiresIn)
}

// writeAccessToken writes the reverified access token: a JSON body for a
// non-browser client, or a refreshed __Host-access_token cookie for a
// browser client. Unlike loginsession.WriteResponse, this never touches
// the refresh_token or device_id cookies — reverify never changes
// either, only the access token itself.
func writeAccessToken(w http.ResponseWriter, r *http.Request, accessToken string, expiresIn int) {
	w.Header().Set("Content-Type", "application/json")

	if loginsession.IsNonBrowser(r) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": accessToken,
			"expires_in":   expiresIn,
		})
		return
	}

	http.SetCookie(w, &http.Cookie{
		Name:     "__Host-access_token",
		Value:    accessToken,
		Path:     "/",
		MaxAge:   expiresIn,
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteStrictMode,
	})
	_ = json.NewEncoder(w).Encode(map[string]any{"expires_in": expiresIn})
}
