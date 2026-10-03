// Package mfaverify completes an MFA login challenge with a factor accepted by its tenant.
package mfaverify

import (
	"context"
	"database/sql"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/auth/authtoken"
	"github.com/djangbahevans/goerp/internal/engine/auth/loginsession"
	"github.com/djangbahevans/goerp/internal/engine/auth/mfatoken"
	"github.com/djangbahevans/goerp/internal/engine/auth/password"
	"github.com/djangbahevans/goerp/internal/engine/authaudit"
	"github.com/djangbahevans/goerp/internal/engine/cache"
	"github.com/djangbahevans/goerp/internal/engine/httperr"
	"github.com/djangbahevans/goerp/internal/engine/mfa"
	"github.com/djangbahevans/goerp/internal/engine/mfa/lockout"
	"github.com/djangbahevans/goerp/internal/engine/mfa/recoverycode"
	"github.com/djangbahevans/goerp/internal/engine/mfa/totp"
	"github.com/djangbahevans/goerp/internal/engine/mfa/webauthn"
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
	webauthn  *webauthn.Service
	audit     *authaudit.Store
}

func NewHandler(mfaTokens *mfatoken.Codec, cacheClient *cache.Client, totpService *totp.Service, recoveryService *recoverycode.Service, tenants *tenant.Store, issuer *authtoken.Issuer, mfaStore *mfa.Store, passkeys *webauthn.Service, audit *authaudit.Store) *Handler {
	return &Handler{
		mfa:       mfaStore,
		webauthn:  passkeys,
		audit:     audit,
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
	MFAToken   string         `json:"mfa_token"`
	Type       string         `json:"type"`
	Code       string         `json:"code"`
	CeremonyID string         `json:"ceremony_id"`
	Response   jsontext.Value `json:"response"`
	DeviceID   string         `json:"device_id"`
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
		if req.Type == "webauthn" {
			svc, svcErr := h.passkeyService(r, claims)
			if svcErr == nil {
				credentialID, err = svc.FinishLoginTx(ctx, tx, claims.Subject, req.CeremonyID, claims.Subject, req.Response, mfa.Scope{TenantID: claims.TenantID}, h.audit, authaudit.Row{IPAddress: loginsession.ClientIP(r), UserAgent: r.UserAgent()})
				valid = err == nil
				if webauthn.InvalidAssertion(err) {
					err = nil
				}
			} else if errors.Is(svcErr, webauthn.ErrInvalidOrigin) {
				valid = false
			} else {
				err = svcErr
			}
		} else {
			valid, credentialID, err = VerifyCodeTx(ctx, tx, h.totp, h.recovery, req.Type, claims.Subject, req.Code, mfa.Scope{TenantID: claims.TenantID})
		}

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

func (h *Handler) passkeyService(r *http.Request, claims *mfatoken.Claims) (*webauthn.Service, error) {
	if h.webauthn == nil {
		return nil, webauthn.ErrInvalidOrigin
	}

	u, err := url.Parse("https://" + r.Host)
	if err != nil {
		return nil, webauthn.ErrInvalidOrigin
	}

	resolved, err := h.tenants.GetByDomain(r.Context(), strings.ToLower(u.Hostname()))
	if err != nil {
		if errors.Is(err, tenant.ErrTenantNotFound) {
			return nil, webauthn.ErrInvalidOrigin
		}

		return nil, err
	}

	if resolved.ID != claims.TenantID || resolved.Status != tenant.StatusActive {
		return nil, webauthn.ErrInvalidOrigin
	}

	return h.webauthn.ForRequest(r.Host, r.Header.Get("Origin"), "login:"+claims.Txn)
}

func (h *Handler) Options(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	var req verifyRequest
	if err := json.UnmarshalRead(r.Body, &req); err != nil {
		httperr.Write(r.Context(), w, http.StatusBadRequest, "invalid_request", "malformed request body")
		return
	}

	claims, err := h.mfaTokens.Verify(req.MFAToken)
	if err != nil || r.Header.Get("Origin") != claims.Origin {
		writeInvalidToken(w, r)
		return
	}

	_, consumed, err := h.cache.Get(r.Context(), consumedKey(claims.Txn))
	if err != nil {
		httperr.Write(r.Context(), w, http.StatusInternalServerError, "internal_error", "mfa verification failed")
		return
	}

	if consumed {
		writeInvalidToken(w, r)
		return
	}

	svc, err := h.passkeyService(r, claims)
	if errors.Is(err, webauthn.ErrInvalidOrigin) {
		writeInvalidToken(w, r)
		return
	}

	if err != nil {
		httperr.Write(r.Context(), w, http.StatusInternalServerError, "internal_error", "mfa verification failed")
		return
	}

	locked, err := h.lockout.Locked(r.Context(), claims.Subject, claims.TenantID)
	if err != nil {
		httperr.Write(r.Context(), w, http.StatusInternalServerError, "internal_error", "mfa verification failed")
		return
	}

	if locked {
		httperr.Write(r.Context(), w, http.StatusLocked, "mfa_locked", "too many failed MFA attempts; try again later")
		return
	}

	options, id, err := svc.BeginLogin(r.Context(), claims.Subject, claims.Subject, mfa.Scope{TenantID: claims.TenantID})
	if errors.Is(err, webauthn.ErrNoEnrolledCredentials) {
		httperr.Write(r.Context(), w, http.StatusBadRequest, "mfa_not_enrolled", "no passkey enrolled for this relying party")
		return
	}

	if err != nil {
		httperr.Write(r.Context(), w, http.StatusInternalServerError, "internal_error", "mfa verification failed")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.MarshalWrite(w, map[string]any{"ceremony_id": id, "options": jsontext.Value(options)})
}
