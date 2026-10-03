// Package mfareverify refreshes an authenticated session with a factor accepted by its tenant.
package mfareverify

import (
	"database/sql"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"net/http"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/auth/authcheck"
	"github.com/djangbahevans/goerp/internal/engine/auth/authtoken"
	"github.com/djangbahevans/goerp/internal/engine/auth/loginsession"
	"github.com/djangbahevans/goerp/internal/engine/auth/mfaverify"
	"github.com/djangbahevans/goerp/internal/engine/auth/session"
	"github.com/djangbahevans/goerp/internal/engine/authaudit"
	"github.com/djangbahevans/goerp/internal/engine/httperr"
	"github.com/djangbahevans/goerp/internal/engine/mfa"
	"github.com/djangbahevans/goerp/internal/engine/mfa/lockout"
	"github.com/djangbahevans/goerp/internal/engine/mfa/recoverycode"
	"github.com/djangbahevans/goerp/internal/engine/mfa/totp"
	"github.com/djangbahevans/goerp/internal/engine/mfa/webauthn"
	tenantresolve "github.com/djangbahevans/goerp/internal/engine/tenant/resolve"
)

const maxBodyBytes = 64 * 1024

type Handler struct {
	mfa      *mfa.Store
	tenants  *tenantresolve.Resolver
	auth     *authcheck.Checker
	sessions *session.Store
	issuer   *authtoken.Issuer
	totp     *totp.Service
	recovery *recoverycode.Service
	lockout  *lockout.Counter
	webauthn *webauthn.Service
	audit    *authaudit.Store
}

func NewHandler(tenants *tenantresolve.Resolver, auth *authcheck.Checker, sessions *session.Store, issuer *authtoken.Issuer, totpService *totp.Service, recoveryService *recoverycode.Service, lockoutCounter *lockout.Counter, mfaStore *mfa.Store, passkeys *webauthn.Service, audit *authaudit.Store) *Handler {
	return &Handler{
		mfa:      mfaStore,
		webauthn: passkeys,
		audit:    audit,
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
	Type       string         `json:"type"`
	Code       string         `json:"code"`
	CeremonyID string         `json:"ceremony_id"`
	Response   jsontext.Value `json:"response"`
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	authCtx, ok := h.authenticate(w, r)
	if !ok {
		return
	}

	ctx := r.Context()
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	var req reverifyRequest
	if err := json.UnmarshalRead(r.Body, &req); err != nil {
		httperr.Write(r.Context(), w, http.StatusBadRequest, "invalid_request", "malformed request body")
		return
	}

	locked, err := h.lockout.Locked(ctx, authCtx.UserID, authCtx.TenantID)
	if err != nil {
		httperr.Write(r.Context(), w, http.StatusInternalServerError, "internal_error", "reverification failed")
		return
	}

	if locked {
		httperr.Write(r.Context(), w, http.StatusLocked, "mfa_locked", "too many failed MFA attempts; try again later")
		return
	}

	now := time.Now()
	var valid bool
	var state session.ReissueState
	err = h.mfa.WithTx(ctx, func(tx *sql.Tx) error {
		if err := h.mfa.LockUserTx(ctx, tx, authCtx.UserID); err != nil {
			return err
		}

		var credentialID string
		var err error
		if req.Type == "webauthn" {
			if h.webauthn != nil {
				svc, svcErr := h.webauthn.ForRequest(r.Host, r.Header.Get("Origin"), "session:"+authCtx.SessionID)
				if svcErr == nil {
					credentialID, err = svc.FinishLoginTx(ctx, tx, authCtx.UserID, req.CeremonyID, authCtx.UserID, req.Response, mfa.Scope{TenantID: authCtx.TenantID}, h.audit, authaudit.Row{ActorUserID: authCtx.UserID, SessionID: authCtx.SessionID, IPAddress: loginsession.ClientIP(r), UserAgent: r.UserAgent()})
					valid = err == nil
					if webauthn.InvalidAssertion(err) {
						err = nil
					}
				} else if !errors.Is(svcErr, webauthn.ErrInvalidOrigin) {
					err = svcErr
				}
			}
		} else {
			valid, credentialID, err = mfaverify.VerifyCodeTx(ctx, tx, h.totp, h.recovery, req.Type, authCtx.UserID, req.Code, mfa.Scope{TenantID: authCtx.TenantID})
		}

		if err != nil || !valid {
			return err
		}

		state, err = h.sessions.UpdateMFAAssuranceTx(ctx, tx, authCtx.SessionID, req.Type, now, credentialID)
		return err
	})
	if err != nil {
		httperr.Write(r.Context(), w, http.StatusInternalServerError, "internal_error", "reverification failed")
		return
	}

	if !valid {
		if err := h.lockout.RecordFailure(ctx, authCtx.UserID, authCtx.TenantID); err != nil {
			httperr.Write(r.Context(), w, http.StatusInternalServerError, "internal_error", "reverification failed")
			return
		}

		httperr.Write(r.Context(), w, http.StatusUnauthorized, "invalid_mfa_code", "invalid MFA code")
		return
	}

	accessToken, expiresIn, err := h.issuer.ReissueAccessToken(authCtx.SessionID, authCtx.TenantID, authCtx.UserID, authCtx.RolesLive, req.Type, &now, state.PasswordChangeRequired, state.ExpiresAt)
	if err != nil {
		httperr.Write(r.Context(), w, http.StatusInternalServerError, "internal_error", "reverification failed")
		return
	}

	if err := h.lockout.Reset(ctx, authCtx.UserID, authCtx.TenantID); err != nil {
		httperr.Write(r.Context(), w, http.StatusInternalServerError, "internal_error", "reverification failed")
		return
	}

	loginsession.WriteReissuedAccessToken(w, r, accessToken, expiresIn, state.Persistent, nil)
}

func (h *Handler) authenticate(w http.ResponseWriter, r *http.Request) (*authcheck.AuthContext, bool) {
	ctx := r.Context()

	tenantCtx, err := h.tenants.ResolveByHost(ctx, r.Host)
	if err != nil {
		switch {
		case errors.Is(err, tenantresolve.ErrTenantNotFound):
			httperr.Write(r.Context(), w, http.StatusNotFound, "not_found", "not found")
		case errors.Is(err, tenantresolve.ErrTenantSuspended):
			httperr.Write(r.Context(), w, http.StatusForbidden, "tenant_suspended", "tenant suspended")
		case errors.Is(err, tenantresolve.ErrTenantOffboarding):
			httperr.Write(r.Context(), w, http.StatusForbidden, "tenant_offboarding", "tenant offboarding")
		default:
			httperr.Write(r.Context(), w, http.StatusInternalServerError, "internal_error", "reverification failed")
		}

		return nil, false
	}

	rawToken := authcheck.ExtractToken(r)
	if rawToken == "" {
		httperr.Write(r.Context(), w, http.StatusUnauthorized, "unauthenticated", "a valid access token is required")
		return nil, false
	}

	authCtx, err := h.auth.AuthenticateAllowingPasswordChange(ctx, rawToken, tenantCtx.TenantID, tenantCtx.Slug, loginsession.ClientIP(r), nil, nil)
	if err != nil || !authCtx.IsAuthenticated || authCtx.AuthMethod != "jwt" {
		httperr.Write(r.Context(), w, http.StatusUnauthorized, "unauthenticated", "a valid access token is required")
		return nil, false
	}

	return authCtx, true
}

func (h *Handler) Options(w http.ResponseWriter, r *http.Request) {
	authCtx, ok := h.authenticate(w, r)
	if !ok {
		return
	}

	if h.webauthn == nil {
		httperr.Write(r.Context(), w, http.StatusInternalServerError, "internal_error", "reverification failed")
		return
	}

	svc, err := h.webauthn.ForRequest(r.Host, r.Header.Get("Origin"), "session:"+authCtx.SessionID)
	if err != nil {
		httperr.Write(r.Context(), w, http.StatusBadRequest, "invalid_request", "invalid passkey origin")
		return
	}

	locked, err := h.lockout.Locked(r.Context(), authCtx.UserID, authCtx.TenantID)
	if err != nil {
		httperr.Write(r.Context(), w, http.StatusInternalServerError, "internal_error", "reverification failed")
		return
	}

	if locked {
		httperr.Write(r.Context(), w, http.StatusLocked, "mfa_locked", "too many failed MFA attempts; try again later")
		return
	}

	options, id, err := svc.BeginLogin(r.Context(), authCtx.UserID, authCtx.UserID, mfa.Scope{TenantID: authCtx.TenantID})
	if errors.Is(err, webauthn.ErrNoEnrolledCredentials) {
		httperr.Write(r.Context(), w, http.StatusBadRequest, "mfa_not_enrolled", "no passkey enrolled for this relying party")
		return
	}

	if err != nil {
		httperr.Write(r.Context(), w, http.StatusInternalServerError, "internal_error", "reverification failed")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.MarshalWrite(w, map[string]any{"ceremony_id": id, "options": jsontext.Value(options)})
}
