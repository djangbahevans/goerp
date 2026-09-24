// Package mfaenroll implements TOTP enrollment — auth-internals.md §8 "MFA
// enrollment": POST /auth/mfa/enroll/totp holds a new secret pending, and
// POST /auth/mfa/enroll/totp/confirm stores it once the user proves their
// authenticator produces valid codes, issuing recovery codes with the
// user's first factor and marking the current session MFA-verified.
//
// Both routes run on the user's normal session and resolve tenant and
// token themselves, the same way mfareverify does. Step 9 exempts
// /auth/mfa/enroll*, so a user facing mfa_setup_required can reach them.
package mfaenroll

import (
	"context"
	"database/sql"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"net/http"
	"slices"
	"time"

	"github.com/rs/zerolog/log"

	"github.com/djangbahevans/goerp/internal/engine/auth/authcheck"
	"github.com/djangbahevans/goerp/internal/engine/auth/authtoken"
	"github.com/djangbahevans/goerp/internal/engine/auth/loginsession"
	"github.com/djangbahevans/goerp/internal/engine/auth/session"
	"github.com/djangbahevans/goerp/internal/engine/authaudit"
	"github.com/djangbahevans/goerp/internal/engine/mfa"
	"github.com/djangbahevans/goerp/internal/engine/mfa/enforce"
	"github.com/djangbahevans/goerp/internal/engine/mfa/recoverycode"
	"github.com/djangbahevans/goerp/internal/engine/mfa/totp"
	tenantresolve "github.com/djangbahevans/goerp/internal/engine/tenant/resolve"
	"github.com/djangbahevans/goerp/internal/engine/user"
)

const maxBodyBytes = 64 * 1024

// AuditRecorder is satisfied by authaudit.Store.
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
}

func NewHandlers(tenants *tenantresolve.Resolver, auth *authcheck.Checker, users *user.Store, mfaStore *mfa.Store, sessions *session.Store, issuer *authtoken.Issuer, totpService *totp.Service, recoveryService *recoverycode.Service, audit AuditRecorder) *Handlers {
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
	}
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

func writeInternal(w http.ResponseWriter) {
	writeJSONError(w, http.StatusInternalServerError, "internal_error", "mfa enrollment failed")
}

// authenticate resolves the tenant from Host and validates the access
// token, writing the error response itself when either fails.
func (h *Handlers) authenticate(w http.ResponseWriter, r *http.Request) (*authcheck.AuthContext, bool) {
	ctx := r.Context()
	tenantCtx, err := h.tenants.ResolveByHost(ctx, r.Host)
	if err != nil {
		switch {
		case errors.Is(err, tenantresolve.ErrTenantNotFound):
			writeJSONError(w, http.StatusNotFound, "not_found", "not found")
		case errors.Is(err, tenantresolve.ErrTenantSuspended):
			writeJSONError(w, http.StatusForbidden, "tenant_suspended", "tenant suspended")
		case errors.Is(err, tenantresolve.ErrTenantOffboarding):
			writeJSONError(w, http.StatusForbidden, "tenant_offboarding", "tenant offboarding")
		default:
			writeInternal(w)
		}
		return nil, false
	}

	rawToken := authcheck.ExtractToken(r)
	if rawToken == "" {
		writeJSONError(w, http.StatusUnauthorized, "unauthenticated", "a valid access token is required")
		return nil, false
	}
	authCtx, err := h.auth.Authenticate(ctx, rawToken, tenantCtx.TenantID, tenantCtx.Slug, loginsession.ClientIP(r), nil, nil)
	if err != nil || !authCtx.IsAuthenticated || authCtx.AuthMethod != "jwt" {
		writeJSONError(w, http.StatusUnauthorized, "unauthenticated", "a valid access token is required")
		return nil, false
	}
	return authCtx, true
}

// Begin serves POST /auth/mfa/enroll/totp.
func (h *Handlers) Begin(w http.ResponseWriter, r *http.Request) {
	authCtx, ok := h.authenticate(w, r)
	if !ok {
		return
	}
	ctx := r.Context()

	u, err := h.users.GetByID(ctx, authCtx.UserID)
	if err != nil {
		if errors.Is(err, user.ErrUserNotFound) {
			writeJSONError(w, http.StatusUnauthorized, "unauthenticated", "a valid access token is required")
			return
		}
		writeInternal(w)
		return
	}

	creds, err := h.mfa.ListActiveByUser(ctx, authCtx.UserID)
	if err != nil {
		writeInternal(w)
		return
	}
	if !h.stepUpSatisfied(w, r, authCtx, creds) {
		return
	}

	pending, err := h.totp.BeginEnrollment(ctx, authCtx.UserID, u.Email)
	if err != nil {
		writeInternal(w)
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
	EnrollmentID string  `json:"enrollment_id"`
	Code         string  `json:"code"`
	Label        *string `json:"label"`
}

// Confirm serves POST /auth/mfa/enroll/totp/confirm.
func (h *Handlers) Confirm(w http.ResponseWriter, r *http.Request) {
	authCtx, ok := h.authenticate(w, r)
	if !ok {
		return
	}
	ctx := r.Context()

	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	var req confirmRequest
	if err := json.UnmarshalRead(r.Body, &req); err != nil || req.EnrollmentID == "" || req.Code == "" {
		writeJSONError(w, http.StatusBadRequest, "invalid_request", `"enrollment_id" and "code" are required`)
		return
	}

	creds, err := h.mfa.ListActiveByUser(ctx, authCtx.UserID)
	if err != nil {
		writeInternal(w)
		return
	}
	if !h.stepUpSatisfied(w, r, authCtx, creds) {
		return
	}

	verified, err := h.totp.CheckEnrollmentCode(ctx, authCtx.UserID, req.EnrollmentID, req.Code)
	switch {
	case errors.Is(err, totp.ErrEnrollmentNotFound):
		writeJSONError(w, http.StatusNotFound, "mfa_enrollment_not_found", "enrollment not found or expired; start again")
		return
	case errors.Is(err, totp.ErrInvalidCode):
		writeJSONError(w, http.StatusBadRequest, "invalid_mfa_code", "invalid MFA code")
		return
	case err != nil:
		writeInternal(w)
		return
	}

	// bcrypt for ten codes takes seconds, so hash before the transaction
	// when the user looks to need codes; the locked re-check inside decides.
	var codes *recoverycode.Set
	if !slices.ContainsFunc(creds, func(c *mfa.Credential) bool { return c.Type == mfa.CredentialRecoveryCode }) {
		set, err := recoverycode.Prepare()
		if err != nil {
			writeInternal(w)
			return
		}
		codes = &set
	}

	now := time.Now()
	var credentialID string
	// any, not []string: json/v2 writes a nil slice as [], and the response
	// uses null for "the user already had recovery codes".
	var issued any
	var persistent bool
	err = h.mfa.WithTx(ctx, func(tx *sql.Tx) error {
		if err := h.mfa.LockUserTx(ctx, tx, authCtx.UserID); err != nil {
			return err
		}
		cred, err := h.mfa.InsertTx(ctx, tx, authCtx.UserID, mfa.CredentialTOTP, verified.Secret, req.Label)
		if err != nil {
			return err
		}
		credentialID = cred.ID

		hasCodes, err := h.mfa.HasActiveOfTypeTx(ctx, tx, authCtx.UserID, mfa.CredentialRecoveryCode)
		if err != nil {
			return err
		}
		if !hasCodes && codes != nil {
			if err := h.recovery.InsertTx(ctx, tx, authCtx.UserID, *codes); err != nil {
				return err
			}
			issued = codes.Codes
		}

		persistent, err = h.sessions.UpdateMFAAssuranceTx(ctx, tx, authCtx.SessionID, string(mfa.CredentialTOTP), now, credentialID)
		if err != nil {
			return err
		}
		// Last, so a failed write above leaves the enrollment pending for a retry.
		return h.totp.ClaimEnrollment(ctx, verified)
	})
	if err != nil {
		if errors.Is(err, totp.ErrEnrollmentNotFound) {
			writeJSONError(w, http.StatusNotFound, "mfa_enrollment_not_found", "enrollment not found or expired; start again")
			return
		}
		if errors.Is(err, session.ErrSessionNotFound) {
			writeJSONError(w, http.StatusUnauthorized, "unauthenticated", "a valid access token is required")
			return
		}
		writeInternal(w)
		return
	}

	accessToken, expiresIn, err := h.issuer.ReissueAccessToken(authCtx.SessionID, authCtx.TenantID, authCtx.UserID, authCtx.RolesLive, string(mfa.CredentialTOTP), &now)
	if err != nil {
		writeInternal(w)
		return
	}

	h.recordAudit(ctx, r, authCtx, credentialID)

	loginsession.WriteReissuedAccessToken(w, r, accessToken, expiresIn, persistent, map[string]any{
		"recovery_codes": issued,
	})
}

// stepUpSatisfied requires a user who already holds a TOTP or WebAuthn
// factor to have verified MFA recently in this session before adding
// another, writing the 403 itself when they haven't. First-factor setup,
// the forced-enrollment case, needs no step-up.
func (h *Handlers) stepUpSatisfied(w http.ResponseWriter, r *http.Request, authCtx *authcheck.AuthContext, creds []*mfa.Credential) bool {
	enrolled := slices.ContainsFunc(creds, func(c *mfa.Credential) bool {
		return c.Type == mfa.CredentialTOTP || c.Type == mfa.CredentialWebAuthn
	})
	if !enrolled {
		return true
	}
	decision, err := h.auth.StepUpDecision(r.Context(), authCtx.TenantID, authCtx)
	if err != nil {
		writeInternal(w)
		return false
	}
	if decision != enforce.Allowed {
		writeJSONError(w, http.StatusForbidden, string(decision), "verify your existing MFA factor before adding another")
		return false
	}
	return true
}

func (h *Handlers) recordAudit(ctx context.Context, r *http.Request, authCtx *authcheck.AuthContext, credentialID string) {
	if h.audit == nil {
		log.Warn().Msg("mfaenroll: no audit recorder wired, mfa.enrolled not recorded")
		return
	}
	metadata, err := json.Marshal(map[string]string{"credential_id": credentialID, "type": string(mfa.CredentialTOTP)})
	if err != nil {
		log.Warn().Err(err).Msg("mfaenroll: encode audit metadata failed")
		return
	}
	if err := h.audit.Insert(ctx, authaudit.Row{
		EventType: "mfa.enrolled",
		TenantID:  authCtx.TenantID,
		UserID:    authCtx.UserID,
		SessionID: authCtx.SessionID,
		IPAddress: loginsession.ClientIP(r),
		UserAgent: r.UserAgent(),
		Success:   true,
		Metadata:  metadata,
	}); err != nil {
		log.Warn().Err(err).Msg("mfaenroll: audit insert failed")
	}
}
