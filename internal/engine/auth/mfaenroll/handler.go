// Package mfaenroll confirms tenant-bound MFA enrollments and issues scoped recovery codes.
package mfaenroll

import (
	"context"
	"database/sql"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"net/http"
	"time"

	"github.com/rs/zerolog/log"

	"github.com/djangbahevans/goerp/internal/engine/auth/authcheck"
	"github.com/djangbahevans/goerp/internal/engine/auth/authtoken"
	"github.com/djangbahevans/goerp/internal/engine/auth/loginsession"
	"github.com/djangbahevans/goerp/internal/engine/auth/session"
	"github.com/djangbahevans/goerp/internal/engine/authaudit"
	"github.com/djangbahevans/goerp/internal/engine/httperr"
	"github.com/djangbahevans/goerp/internal/engine/mfa"
	"github.com/djangbahevans/goerp/internal/engine/mfa/enforce"
	"github.com/djangbahevans/goerp/internal/engine/mfa/recoverycode"
	"github.com/djangbahevans/goerp/internal/engine/mfa/totp"
	"github.com/djangbahevans/goerp/internal/engine/mfa/webauthn"
	tenantresolve "github.com/djangbahevans/goerp/internal/engine/tenant/resolve"
	"github.com/djangbahevans/goerp/internal/engine/user"
)

const maxBodyBytes = 64 * 1024

type AuditRecorder interface {
	Insert(ctx context.Context, row authaudit.Row) error
}

type Handlers struct {
	tenants  *tenantresolve.Resolver
	auth     *authcheck.Checker
	users    *user.Store
	mfa      *mfa.Store
	sessions *session.Store
	issuer   *authtoken.Issuer
	totp     *totp.Service
	recovery *recoverycode.Service
	audit    AuditRecorder
	webauthn *webauthn.Service
}

func NewHandlers(tenants *tenantresolve.Resolver, auth *authcheck.Checker, users *user.Store, mfaStore *mfa.Store, sessions *session.Store, issuer *authtoken.Issuer, totpService *totp.Service, recoveryService *recoverycode.Service, audit AuditRecorder, passkeys *webauthn.Service) *Handlers {
	return &Handlers{
		tenants:  tenants,
		auth:     auth,
		users:    users,
		mfa:      mfaStore,
		sessions: sessions,
		issuer:   issuer,
		totp:     totpService,
		recovery: recoveryService,
		audit:    audit,
		webauthn: passkeys,
	}
}

func writeJSON(w http.ResponseWriter, v any) {
	_ = json.MarshalWrite(w, v, jsontext.EscapeForHTML(true), jsontext.EscapeForJS(true))
}

func writeInternal(w http.ResponseWriter, r *http.Request) {
	httperr.Write(r.Context(), w, http.StatusInternalServerError, "internal_error", "mfa enrollment failed")
}

func (h *Handlers) authenticate(w http.ResponseWriter, r *http.Request) (*authcheck.AuthContext, bool) {
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
			writeInternal(w, r)
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

func (h *Handlers) Begin(w http.ResponseWriter, r *http.Request) {
	authCtx, ok := h.authenticate(w, r)
	if !ok {
		return
	}

	ctx := r.Context()

	u, err := h.users.GetByID(ctx, authCtx.UserID)
	if err != nil {
		if errors.Is(err, user.ErrUserNotFound) {
			httperr.Write(r.Context(), w, http.StatusUnauthorized, "unauthenticated", "a valid access token is required")
			return
		}

		writeInternal(w, r)
		return
	}

	creds, err := h.mfa.ListAccepted(ctx, authCtx.UserID, mfa.Scope{TenantID: authCtx.TenantID})
	if err != nil {
		writeInternal(w, r)
		return
	}

	if !h.stepUpSatisfied(w, r, authCtx, creds) {
		return
	}

	pending, err := h.totp.BeginEnrollment(ctx, authCtx.UserID, u.Email, authCtx.TenantID)
	if err != nil {
		writeInternal(w, r)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	writeJSON(w, map[string]any{
		"enrollment_id": pending.ID,
		"qr_svg":        string(pending.QRSVG),
		"secret":        pending.Secret,
	})
}

type confirmRequest struct {
	EnrollmentID string         `json:"enrollment_id"`
	Code         string         `json:"code"`
	Label        *string        `json:"label"`
	CeremonyID   string         `json:"ceremony_id"`
	Response     jsontext.Value `json:"response"`
}

func (h *Handlers) Confirm(w http.ResponseWriter, r *http.Request) {
	h.confirm(w, r, mfa.CredentialTOTP)
}

func (h *Handlers) ConfirmWebAuthn(w http.ResponseWriter, r *http.Request) {
	h.confirm(w, r, mfa.CredentialWebAuthn)
}

func (h *Handlers) confirm(w http.ResponseWriter, r *http.Request, method mfa.CredentialType) {
	authCtx, ok := h.authenticate(w, r)
	if !ok {
		return
	}

	ctx := r.Context()

	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	var req confirmRequest
	if err := json.UnmarshalRead(r.Body, &req); err != nil || (method == mfa.CredentialTOTP && (req.EnrollmentID == "" || req.Code == "")) || (method == mfa.CredentialWebAuthn && (req.CeremonyID == "" || len(req.Response) == 0)) {
		httperr.Write(r.Context(), w, http.StatusBadRequest, "invalid_request", "enrollment identifier and factor response are required")
		return
	}

	creds, err := h.mfa.ListAccepted(ctx, authCtx.UserID, mfa.Scope{TenantID: authCtx.TenantID})
	if err != nil {
		writeInternal(w, r)
		return
	}

	if !h.stepUpSatisfied(w, r, authCtx, creds) {
		return
	}

	var verified *totp.VerifiedEnrollment
	var registration *webauthn.Registration
	var passkeys *webauthn.Service
	if method == mfa.CredentialWebAuthn {
		if h.webauthn == nil {
			writeInternal(w, r)
			return
		}

		passkeys, err = h.webauthn.ForRequest(r.Host, r.Header.Get("Origin"), "session:"+authCtx.SessionID)
		if err != nil {
			httperr.Write(ctx, w, http.StatusBadRequest, "invalid_request", "invalid passkey origin")
			return
		}

		u, userErr := h.users.GetByID(ctx, authCtx.UserID)
		if userErr != nil {
			writeInternal(w, r)
			return
		}

		registration, err = passkeys.CheckRegistration(ctx, authCtx.UserID, req.CeremonyID, u.Email, req.Response, mfa.Scope{TenantID: authCtx.TenantID})
	} else {
		verified, err = h.totp.CheckEnrollmentCode(ctx, authCtx.UserID, req.EnrollmentID, req.Code, authCtx.TenantID)
	}

	switch {
	case errors.Is(err, totp.ErrEnrollmentNotFound), errors.Is(err, webauthn.ErrCeremonyExpired), errors.Is(err, webauthn.ErrCeremonyUserMismatch):
		httperr.Write(ctx, w, http.StatusNotFound, "mfa_enrollment_not_found", "enrollment not found or expired; start again")
		return
	case errors.Is(err, totp.ErrInvalidCode):
		httperr.Write(ctx, w, http.StatusBadRequest, "invalid_mfa_code", "invalid MFA code")
		return
	case errors.Is(err, webauthn.ErrInvalidResponse):
		httperr.Write(ctx, w, http.StatusBadRequest, "invalid_mfa_response", "invalid passkey response")
		return
	case err != nil:
		writeInternal(w, r)
		return
	}

	// Hash outside the account lock; the locked check decides whether the
	// matching scope needs this set.
	codes, err := recoverycode.Prepare()
	if err != nil {
		writeInternal(w, r)
		return
	}

	now := time.Now()
	var credentialID string
	// any, not []string: json/v2 writes a nil slice as [], and the response
	// uses null for "the user already had recovery codes".
	var issued any
	var state session.ReissueState
	var denial enforce.Decision
	err = h.mfa.WithTx(ctx, func(tx *sql.Tx) error {
		if err := h.mfa.LockUserTx(ctx, tx, authCtx.UserID); err != nil {
			return err
		}

		accepted, err := h.mfa.ListAcceptedTx(ctx, tx, authCtx.UserID, mfa.Scope{TenantID: authCtx.TenantID})
		if err != nil {
			return err
		}

		if mfa.HasFactor(accepted) {
			denial, err = h.auth.StepUpDecision(ctx, authCtx.TenantID, authCtx)
			if err != nil {
				return err
			}

			if denial != enforce.Allowed {
				return errors.New("mfa enrollment requires step-up")
			}
		}

		factorTenant, err := h.mfa.EnrollmentTenantTx(ctx, tx, authCtx.UserID, authCtx.TenantID, authCtx.SessionID)
		if errors.Is(err, sql.ErrNoRows) {
			return session.ErrSessionNotFound
		}

		if err != nil {
			return err
		}

		var cred *mfa.Credential
		if method == mfa.CredentialWebAuthn {
			cred, err = passkeys.InsertRegistrationTx(ctx, tx, registration, authCtx.UserID, factorTenant, req.Label)
		} else {
			cred, err = h.mfa.InsertScopedTx(ctx, tx, authCtx.UserID, factorTenant, method, verified.Secret, req.Label)
		}

		if err != nil {
			return err
		}

		credentialID = cred.ID

		hasCodes, err := h.mfa.HasRecoveryCodesTx(ctx, tx, authCtx.UserID, factorTenant)
		if err != nil {
			return err
		}

		if !hasCodes {
			if err := h.recovery.InsertTx(ctx, tx, authCtx.UserID, factorTenant, codes); err != nil {
				return err
			}

			issued = codes.Codes
		}

		if factorTenant != nil {
			if err := h.mfa.SetResetTx(ctx, tx, authCtx.UserID, authCtx.TenantID, false); err != nil {
				return err
			}
		}

		state, err = h.sessions.UpdateMFAAssuranceTx(ctx, tx, authCtx.SessionID, string(method), now, credentialID)
		if err != nil {
			return err
		}

		// Claim TOTP last so a preceding database failure leaves its enrollment retryable.
		if method == mfa.CredentialTOTP {
			return h.totp.ClaimEnrollment(ctx, verified)
		}

		return nil
	})
	if err != nil {
		if denial != enforce.Allowed {
			httperr.Write(ctx, w, http.StatusForbidden, string(denial), "verify your existing MFA factor before adding another")
			return
		}

		if errors.Is(err, totp.ErrEnrollmentNotFound) {
			httperr.Write(r.Context(), w, http.StatusNotFound, "mfa_enrollment_not_found", "enrollment not found or expired; start again")
			return
		}

		if errors.Is(err, session.ErrSessionNotFound) {
			httperr.Write(r.Context(), w, http.StatusUnauthorized, "unauthenticated", "a valid access token is required")
			return
		}

		writeInternal(w, r)
		return
	}

	accessToken, expiresIn, err := h.issuer.ReissueAccessToken(authCtx.SessionID, authCtx.TenantID, authCtx.UserID, authCtx.RolesLive, string(method), &now, state.PasswordChangeRequired, state.ExpiresAt)
	if err != nil {
		writeInternal(w, r)
		return
	}

	h.recordAudit(ctx, r, authCtx, credentialID, method)

	loginsession.WriteReissuedAccessToken(w, r, accessToken, expiresIn, state.Persistent, map[string]any{
		"recovery_codes": issued,
	})
}

func (h *Handlers) stepUpSatisfied(w http.ResponseWriter, r *http.Request, authCtx *authcheck.AuthContext, creds []*mfa.Credential) bool {
	if !mfa.HasFactor(creds) {
		return true
	}

	decision, err := h.auth.StepUpDecision(r.Context(), authCtx.TenantID, authCtx)
	if err != nil {
		writeInternal(w, r)
		return false
	}

	if decision != enforce.Allowed {
		httperr.Write(r.Context(), w, http.StatusForbidden, string(decision), "verify your existing MFA factor before adding another")
		return false
	}

	return true
}

func (h *Handlers) recordAudit(ctx context.Context, r *http.Request, authCtx *authcheck.AuthContext, credentialID string, method mfa.CredentialType) {
	if h.audit == nil {
		log.Warn().Msg("mfaenroll: no audit recorder wired, mfa.enrolled not recorded")
		return
	}

	metadata, err := json.Marshal(map[string]string{"credential_id": credentialID, "type": string(method)})
	if err != nil {
		log.Warn().Err(err).Msg("mfaenroll: encode audit metadata failed")
		return
	}

	if err := h.audit.Insert(ctx, authaudit.Row{
		EventType:   "mfa.enrolled",
		TenantID:    authCtx.TenantID,
		UserID:      authCtx.UserID,
		ActorUserID: authCtx.UserID,
		SessionID:   authCtx.SessionID,
		IPAddress:   loginsession.ClientIP(r),
		UserAgent:   r.UserAgent(),
		Success:     true,
		Metadata:    metadata,
	}); err != nil {
		log.Warn().Err(err).Msg("mfaenroll: audit insert failed")
	}
}

func (h *Handlers) BeginWebAuthn(w http.ResponseWriter, r *http.Request) {
	authCtx, ok := h.authenticate(w, r)
	if !ok {
		return
	}

	ctx := r.Context()
	creds, err := h.mfa.ListAccepted(ctx, authCtx.UserID, mfa.Scope{TenantID: authCtx.TenantID})
	if err != nil {
		writeInternal(w, r)
		return
	}

	if !h.stepUpSatisfied(w, r, authCtx, creds) {
		return
	}

	if h.webauthn == nil {
		writeInternal(w, r)
		return
	}

	svc, err := h.webauthn.ForRequest(r.Host, r.Header.Get("Origin"), "session:"+authCtx.SessionID)
	if err != nil {
		httperr.Write(ctx, w, http.StatusBadRequest, "invalid_request", "invalid passkey origin")
		return
	}

	u, err := h.users.GetByID(ctx, authCtx.UserID)
	if err != nil {
		writeInternal(w, r)
		return
	}

	options, id, err := svc.BeginRegistration(ctx, authCtx.UserID, u.Email, mfa.Scope{TenantID: authCtx.TenantID})
	if err != nil {
		writeInternal(w, r)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	writeJSON(w, map[string]any{"ceremony_id": id, "options": jsontext.Value(options)})
}
