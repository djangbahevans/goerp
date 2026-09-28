// Package mfafactors implements a user's own MFA factor management —
// auth-internals.md §8 "Managing factors": GET /auth/mfa/factors, POST
// /auth/mfa/factors/{id}/remove and POST
// /auth/mfa/recovery-codes/regenerate. Both changes are confirmed with a
// current MFA code, verified the same way POST /auth/mfa/reverify verifies
// one and counted against the same lockout.
//
// Class A routes that resolve the tenant from Host and authenticate the
// access token themselves, like internal/engine/auth/mfaenroll.
package mfafactors

import (
	"context"
	"database/sql"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"net/http"
	"slices"
	"time"
	"uuid"

	"github.com/rs/zerolog/log"

	"github.com/djangbahevans/goerp/internal/engine/auth/authcheck"
	"github.com/djangbahevans/goerp/internal/engine/auth/loginsession"
	"github.com/djangbahevans/goerp/internal/engine/auth/mfaverify"
	"github.com/djangbahevans/goerp/internal/engine/auth/session"
	"github.com/djangbahevans/goerp/internal/engine/auth/sessionrevoke"
	"github.com/djangbahevans/goerp/internal/engine/authaudit"
	"github.com/djangbahevans/goerp/internal/engine/httperr"
	"github.com/djangbahevans/goerp/internal/engine/mfa"
	"github.com/djangbahevans/goerp/internal/engine/mfa/enforce"
	"github.com/djangbahevans/goerp/internal/engine/mfa/lockout"
	"github.com/djangbahevans/goerp/internal/engine/mfa/recoverycode"
	"github.com/djangbahevans/goerp/internal/engine/mfa/revoke"
	"github.com/djangbahevans/goerp/internal/engine/mfa/totp"
	"github.com/djangbahevans/goerp/internal/engine/route"
	tenantresolve "github.com/djangbahevans/goerp/internal/engine/tenant/resolve"
)

const maxBodyBytes = 64 * 1024

// regenerateRevokeReason matches auth-internals.md §4's revoke_reason
// vocabulary.
const regenerateRevokeReason = "security_event"

// AuditRecorder is satisfied by authaudit.Store.
type AuditRecorder interface {
	Insert(ctx context.Context, row authaudit.Row) error
}

type Handlers struct {
	tenants  *tenantresolve.Resolver
	auth     *authcheck.Checker
	mfa      *mfa.Store
	policies *enforce.Store
	totp     *totp.Service
	recovery *recoverycode.Service
	lockout  *lockout.Counter
	factors  *revoke.Service
	sessions *sessionrevoke.Revoker
	audit    AuditRecorder
}

func NewHandlers(tenants *tenantresolve.Resolver, auth *authcheck.Checker, mfaStore *mfa.Store, policies *enforce.Store, totpService *totp.Service, recoveryService *recoverycode.Service, lockoutCounter *lockout.Counter, factors *revoke.Service, sessions *sessionrevoke.Revoker, audit AuditRecorder) *Handlers {
	return &Handlers{
		tenants:  tenants,
		auth:     auth,
		mfa:      mfaStore,
		policies: policies,
		totp:     totpService,
		recovery: recoveryService,
		lockout:  lockoutCounter,
		factors:  factors,
		sessions: sessions,
		audit:    audit,
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.MarshalWrite(w, v, jsontext.EscapeForHTML(true), jsontext.EscapeForJS(true))
}

func writeInternalError(w http.ResponseWriter, r *http.Request, err error, msg string) {
	log.Error().Err(err).Msg("mfafactors: " + msg)
	httperr.Write(r.Context(), w, http.StatusInternalServerError, "internal_error", "request failed")
}

// authenticate resolves the tenant from Host and validates the access
// token, writing the error response itself when either fails.
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
			writeInternalError(w, r, err, "tenant resolution failed")
		}
		return nil, false
	}

	rawToken := authcheck.ExtractToken(r)
	if rawToken == "" {
		httperr.Write(r.Context(), w, http.StatusUnauthorized, "unauthenticated", "a valid access token is required")
		return nil, false
	}
	authCtx, err := h.auth.Authenticate(ctx, rawToken, tenantCtx.TenantID, tenantCtx.Slug, loginsession.ClientIP(r), nil, nil)
	if err != nil || !authCtx.IsAuthenticated || authCtx.AuthMethod != "jwt" {
		httperr.Write(r.Context(), w, http.StatusUnauthorized, "unauthenticated", "a valid access token is required")
		return nil, false
	}
	return authCtx, true
}

func (h *Handlers) policyApplies(ctx context.Context, authCtx *authcheck.AuthContext) (bool, error) {
	policy, err := h.policies.LoadPolicy(ctx, authCtx.TenantID)
	if err != nil {
		return false, err
	}
	return policy.Applies(authCtx.RolesLive), nil
}

type factorJSON struct {
	ID         string     `json:"id"`
	Type       string     `json:"type"`
	Label      *string    `json:"label"`
	CreatedAt  time.Time  `json:"created_at"`
	LastUsedAt *time.Time `json:"last_used_at"`
}

// List serves GET /auth/mfa/factors.
func (h *Handlers) List(w http.ResponseWriter, r *http.Request) {
	authCtx, ok := h.authenticate(w, r)
	if !ok {
		return
	}
	ctx := r.Context()

	creds, err := h.mfa.ListActiveByUser(ctx, authCtx.UserID)
	if err != nil {
		writeInternalError(w, r, err, "list mfa factors failed")
		return
	}
	required, err := h.policyApplies(ctx, authCtx)
	if err != nil {
		writeInternalError(w, r, err, "load mfa policy failed")
		return
	}

	factors := []factorJSON{}
	codesRemaining := 0
	for _, c := range creds {
		if c.Type == mfa.CredentialRecoveryCode {
			codesRemaining++
			continue
		}
		factors = append(factors, factorJSON{
			ID:         c.ID,
			Type:       string(c.Type),
			Label:      c.Label,
			CreatedAt:  c.CreatedAt,
			LastUsedAt: c.LastUsedAt,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"factors":                  factors,
		"recovery_codes_remaining": codesRemaining,
		"required_by_policy":       required,
	})
}

type codeRequest struct {
	Type string `json:"type"`
	Code string `json:"code"`
}

func readCodeRequest(w http.ResponseWriter, r *http.Request) (codeRequest, bool) {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	var req codeRequest
	if err := json.UnmarshalRead(r.Body, &req); err != nil || req.Type == "" || req.Code == "" {
		httperr.Write(r.Context(), w, http.StatusBadRequest, "invalid_request", `"type" and "code" are required`)
		return codeRequest{}, false
	}
	return req, true
}

// verifyCode checks req against the caller's enrolled factors under the
// same lockout as POST /auth/mfa/reverify, writing the error response
// itself on failure. A recovery code that verifies is consumed.
func (h *Handlers) verifyCode(w http.ResponseWriter, r *http.Request, authCtx *authcheck.AuthContext, req codeRequest) bool {
	ctx := r.Context()
	locked, err := h.lockout.Locked(ctx, authCtx.UserID, authCtx.TenantID)
	if err != nil {
		writeInternalError(w, r, err, "mfa lockout check failed")
		return false
	}
	if locked {
		httperr.Write(r.Context(), w, http.StatusLocked, "mfa_locked", "too many failed MFA attempts; try again later")
		return false
	}

	valid, _, err := mfaverify.VerifyCode(ctx, h.totp, h.recovery, req.Type, authCtx.UserID, req.Code)
	if err != nil {
		writeInternalError(w, r, err, "mfa code verification failed")
		return false
	}
	if !valid {
		if err := h.lockout.RecordFailure(ctx, authCtx.UserID, authCtx.TenantID); err != nil {
			writeInternalError(w, r, err, "record mfa failure failed")
			return false
		}
		httperr.Write(r.Context(), w, http.StatusUnauthorized, "invalid_mfa_code", "invalid MFA code")
		return false
	}
	if err := h.lockout.Reset(ctx, authCtx.UserID, authCtx.TenantID); err != nil {
		writeInternalError(w, r, err, "reset mfa lockout failed")
		return false
	}
	return true
}

// Remove serves POST /auth/mfa/factors/{id}/remove.
func (h *Handlers) Remove(w http.ResponseWriter, r *http.Request) {
	authCtx, ok := h.authenticate(w, r)
	if !ok {
		return
	}
	ctx := r.Context()

	req, ok := readCodeRequest(w, r)
	if !ok {
		return
	}

	factorID := route.ParamsFromContext(ctx)["id"]
	if _, err := uuid.Parse(factorID); err != nil {
		httperr.Write(r.Context(), w, http.StatusNotFound, "mfa_factor_not_found", "MFA factor not found")
		return
	}
	creds, err := h.mfa.ListActiveByUser(ctx, authCtx.UserID)
	if err != nil {
		writeInternalError(w, r, err, "list mfa factors failed")
		return
	}
	factors := slices.DeleteFunc(creds, func(c *mfa.Credential) bool { return !c.Type.IsFactor() })
	if !slices.ContainsFunc(factors, func(c *mfa.Credential) bool { return c.ID == factorID }) {
		httperr.Write(r.Context(), w, http.StatusNotFound, "mfa_factor_not_found", "MFA factor not found")
		return
	}
	required, err := h.policyApplies(ctx, authCtx)
	if err != nil {
		writeInternalError(w, r, err, "load mfa policy failed")
		return
	}
	// Checked before the code, so a refused removal spends no recovery code
	// and no lockout attempt.
	if required && len(factors) == 1 {
		writeRequiredByPolicy(w, r)
		return
	}

	if !h.verifyCode(w, r, authCtx, req) {
		return
	}

	switch err := h.factors.RevokeFactor(ctx, authCtx.UserID, factorID, required); {
	case errors.Is(err, mfa.ErrCredentialNotFound):
		httperr.Write(r.Context(), w, http.StatusNotFound, "mfa_factor_not_found", "MFA factor not found")
		return
	case errors.Is(err, revoke.ErrRequiredByPolicy):
		writeRequiredByPolicy(w, r)
		return
	case err != nil:
		writeInternalError(w, r, err, "revoke mfa factor failed")
		return
	}

	h.recordAudit(r, authCtx, "mfa.revoked", map[string]string{"credential_id": factorID})
	w.WriteHeader(http.StatusNoContent)
}

func writeRequiredByPolicy(w http.ResponseWriter, r *http.Request) {
	httperr.Write(r.Context(), w, http.StatusConflict, "mfa_required_by_policy", "your organisation requires two-factor authentication")
}

// RegenerateRecoveryCodes serves POST /auth/mfa/recovery-codes/regenerate.
func (h *Handlers) RegenerateRecoveryCodes(w http.ResponseWriter, r *http.Request) {
	authCtx, ok := h.authenticate(w, r)
	if !ok {
		return
	}
	ctx := r.Context()

	req, ok := readCodeRequest(w, r)
	if !ok {
		return
	}

	creds, err := h.mfa.ListActiveByUser(ctx, authCtx.UserID)
	if err != nil {
		writeInternalError(w, r, err, "list mfa factors failed")
		return
	}
	if !slices.ContainsFunc(creds, func(c *mfa.Credential) bool { return c.Type.IsFactor() }) {
		writeNotEnrolled(w, r)
		return
	}

	if !h.verifyCode(w, r, authCtx, req) {
		return
	}

	set, err := recoverycode.Prepare()
	if err != nil {
		writeInternalError(w, r, err, "generate recovery codes failed")
		return
	}
	// The other sessions are revoked in the same transaction, so a failure
	// can't replace the codes without the caller ever seeing the new set.
	var revoked []session.RevokedFamily
	err = h.mfa.WithTx(ctx, func(tx *sql.Tx) error {
		if err := h.recovery.RegenerateTx(ctx, tx, authCtx.UserID, set); err != nil {
			return err
		}
		revoked, err = h.sessions.RevokeOtherFamiliesForUserInTenantTx(ctx, tx, authCtx.UserID, authCtx.TenantID, authCtx.SessionID, regenerateRevokeReason)
		return err
	})
	switch {
	case errors.Is(err, recoverycode.ErrNotEnrolled):
		writeNotEnrolled(w, r)
		return
	case err != nil:
		writeInternalError(w, r, err, "regenerate recovery codes failed")
		return
	}
	var revokedIDs []string
	for _, f := range revoked {
		revokedIDs = append(revokedIDs, f.RowIDs...)
	}
	// The codes are already committed, so they're returned even if an
	// already-revoked session's access token can't be blocklisted.
	if err := h.sessions.Blocklist(ctx, revokedIDs); err != nil {
		log.Error().Err(err).Msg("mfafactors: blocklist sessions after recovery code regeneration failed")
	}

	h.recordAudit(r, authCtx, "mfa.recovery_codes_regenerated", nil)

	writeJSON(w, http.StatusOK, map[string]any{"recovery_codes": set.Codes})
}

func writeNotEnrolled(w http.ResponseWriter, r *http.Request) {
	httperr.Write(r.Context(), w, http.StatusConflict, "mfa_not_enrolled", "no two-factor authentication method is set up")
}

func (h *Handlers) recordAudit(r *http.Request, authCtx *authcheck.AuthContext, eventType string, metadata map[string]string) {
	if h.audit == nil {
		log.Warn().Str("event", eventType).Msg("mfafactors: no audit recorder wired, event not recorded")
		return
	}
	var raw []byte
	if metadata != nil {
		var err error
		if raw, err = json.Marshal(metadata); err != nil {
			log.Warn().Err(err).Msg("mfafactors: encode audit metadata failed")
			return
		}
	}
	if err := h.audit.Insert(r.Context(), authaudit.Row{
		EventType:   eventType,
		TenantID:    authCtx.TenantID,
		UserID:      authCtx.UserID,
		ActorUserID: authCtx.UserID,
		SessionID:   authCtx.SessionID,
		IPAddress:   loginsession.ClientIP(r),
		UserAgent:   r.UserAgent(),
		Success:     true,
		Metadata:    raw,
	}); err != nil {
		log.Warn().Err(err).Str("event", eventType).Msg("mfafactors: audit insert failed")
	}
}
