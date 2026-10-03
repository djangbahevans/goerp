// Package mfaverify completes an MFA login challenge with a factor accepted by its tenant.
package mfaverify

import (
	"context"
	"database/sql"
	"encoding/json/v2"
	"errors"
	"net/http"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/auth/authtoken"
	"github.com/djangbahevans/goerp/internal/engine/auth/loginsession"
	"github.com/djangbahevans/goerp/internal/engine/auth/mfatoken"
	"github.com/djangbahevans/goerp/internal/engine/auth/password"
	"github.com/djangbahevans/goerp/internal/engine/cache"
	"github.com/djangbahevans/goerp/internal/engine/httperr"
	"github.com/djangbahevans/goerp/internal/engine/mfa"
	"github.com/djangbahevans/goerp/internal/engine/mfa/lockout"
	"github.com/djangbahevans/goerp/internal/engine/mfa/recoverycode"
	"github.com/djangbahevans/goerp/internal/engine/mfa/totp"
	"github.com/djangbahevans/goerp/internal/engine/tenant"
)

const maxBodyBytes = 64 * 1024

type Handler struct {
	mfa       *mfa.Store
	mfaTokens *mfatoken.Codec
	cache     *cache.Client
	totp      *totp.Service
	recovery  *recoverycode.Service
	tenants   *tenant.Store
	issuer    *authtoken.Issuer
	lockout   *lockout.Counter
}

func NewHandler(mfaTokens *mfatoken.Codec, cacheClient *cache.Client, totpService *totp.Service, recoveryService *recoverycode.Service, tenants *tenant.Store, issuer *authtoken.Issuer, mfaStore *mfa.Store) *Handler {
	return &Handler{
		mfa:       mfaStore,
		mfaTokens: mfaTokens,
		cache:     cacheClient,
		totp:      totpService,
		recovery:  recoveryService,
		tenants:   tenants,
		issuer:    issuer,
		lockout:   lockout.NewCounter(cacheClient),
	}
}

type verifyRequest struct {
	MFAToken string `json:"mfa_token"`
	Type     string `json:"type"`
	Code     string `json:"code"`
	// DeviceID is read only for a non-browser client (X-Client-Type: cli)
	// completing its first-ever login — a browser client's device_id
	// travels as a cookie instead, same as POST /auth/login's own
	// device_id field.
	DeviceID string `json:"device_id"`
}

func writeInvalidToken(w http.ResponseWriter, r *http.Request) {
	httperr.Write(r.Context(), w, http.StatusUnauthorized, "invalid_mfa_token", "invalid, expired, or already-used mfa_token")
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	var req verifyRequest
	if err := json.UnmarshalRead(r.Body, &req); err != nil {
		httperr.Write(r.Context(), w, http.StatusBadRequest, "invalid_request", "malformed request body")
		return
	}

	ctx := r.Context()

	claims, err := h.mfaTokens.Verify(req.MFAToken)
	if err != nil {
		writeInvalidToken(w, r)
		return
	}

	if r.Header.Get("Origin") != claims.Origin {
		writeInvalidToken(w, r)
		return
	}

	remaining := time.Until(claims.ExpiresAt.Time)
	if remaining <= 0 {
		writeInvalidToken(w, r)
		return
	}

	claimed, err := h.cache.SetNXWithTTL(ctx, consumedKey(claims.Txn), "1", remaining)
	if err != nil {
		httperr.Write(r.Context(), w, http.StatusInternalServerError, "internal_error", "mfa verification failed")
		return
	}

	if !claimed {
		writeInvalidToken(w, r)
		return
	}

	locked, err := h.lockout.Locked(ctx, claims.Subject, claims.TenantID)
	if err != nil {
		httperr.Write(r.Context(), w, http.StatusInternalServerError, "internal_error", "mfa verification failed")
		return
	}

	if locked {
		httperr.Write(r.Context(), w, http.StatusLocked, "mfa_locked", "too many failed MFA attempts; try again later")
		return
	}

	t, err := h.tenants.GetByID(ctx, claims.TenantID)
	if err != nil {
		httperr.Write(ctx, w, http.StatusInternalServerError, "internal_error", "mfa verification failed")
		return
	}

	nonBrowser := loginsession.IsNonBrowser(r)
	deviceID, deviceIDIsFresh := loginsession.ResolveDeviceID(r, req.DeviceID, nonBrowser)
	now := time.Now()
	passwordPolicy := claims.PasswordPolicyResult()
	var valid bool
	var tokens *authtoken.Tokens
	err = h.mfa.WithTx(ctx, func(tx *sql.Tx) error {
		if err := h.mfa.LockUserTx(ctx, tx, claims.Subject); err != nil {
			return err
		}

		var credentialID string
		var err error
		valid, credentialID, err = VerifyCodeTx(ctx, tx, h.totp, h.recovery, req.Type, claims.Subject, req.Code, mfa.Scope{TenantID: claims.TenantID})
		if err != nil || !valid {
			return err
		}

		tokens, err = h.issuer.IssueTx(ctx, tx, authtoken.LoginParams{
			UserID:          claims.Subject,
			TenantSlug:      t.Slug,
			DeviceID:        deviceID,
			UserAgent:       r.UserAgent(),
			IPAddress:       loginsession.ClientIP(r),
			Persistent:      nonBrowser || claims.Remember,
			MFAMethod:       req.Type,
			MFAVerifiedAt:   &now,
			MFACredentialID: credentialID,

			PasswordChangeRequired: passwordPolicy.Outcome == password.ChangeRequired,
		})
		return err
	})
	if errors.Is(err, authtoken.ErrIPNotAllowed) {
		httperr.Write(r.Context(), w, http.StatusForbidden, "ip_not_allowed", "signing in to this tenant is not allowed from your network")
		return
	}

	if err != nil {
		httperr.Write(r.Context(), w, http.StatusInternalServerError, "internal_error", "mfa verification failed")
		return
	}

	if !valid {
		if err := h.lockout.RecordFailure(ctx, claims.Subject, claims.TenantID); err != nil {
			httperr.Write(ctx, w, http.StatusInternalServerError, "internal_error", "mfa verification failed")
			return
		}

		httperr.Write(ctx, w, http.StatusUnauthorized, "invalid_mfa_code", "invalid MFA code")
		return
	}

	// Reset the attempt counter on success so a later, separate login
	// attempt doesn't inherit an in-progress failure count.
	if err := h.lockout.Reset(ctx, claims.Subject, claims.TenantID); err != nil {
		httperr.Write(r.Context(), w, http.StatusInternalServerError, "internal_error", "mfa verification failed")
		return
	}

	loginsession.WriteResponse(w, tokens, deviceID, deviceIDIsFresh, nonBrowser, passwordPolicy)
}

func VerifyCode(ctx context.Context, totpSvc *totp.Service, recoverySvc *recoverycode.Service, mfaType, userID, code string, scope mfa.Scope) (valid bool, credentialID string, err error) {
	switch mfa.CredentialType(mfaType) {
	case mfa.CredentialTOTP:
		return totpSvc.Verify(ctx, userID, code, scope)
	case mfa.CredentialRecoveryCode:
		return recoverySvc.Verify(ctx, userID, code, scope)
	default:
		return false, "", nil
	}
}

func VerifyCodeTx(ctx context.Context, tx *sql.Tx, totpSvc *totp.Service, recoverySvc *recoverycode.Service, mfaType, userID, code string, scope mfa.Scope) (bool, string, error) {
	if mfa.CredentialType(mfaType) == mfa.CredentialRecoveryCode {
		return recoverySvc.VerifyTx(ctx, tx, userID, code, scope)
	}

	return VerifyCode(ctx, totpSvc, recoverySvc, mfaType, userID, code, scope)
}

func consumedKey(txn string) string {
	return "auth:mfa_token:consumed:" + txn
}
