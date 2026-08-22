// Package mfaverify implements POST /auth/mfa/verify — auth-internals.md
// §8 "MFA token flow"'s 8-step order: validate the mfa_token's signature
// and expiry, check its Origin claim against the request, atomically
// consume it (single-use), check the (user_id, tenant_id) lockout
// counter, verify the submitted code against an enrolled factor, and on
// success issue the full session with real MFA assurance columns set.
//
// Verifying against a specific factor type is delegated to totp.Service
// and recoverycode.Service — this handler only dispatches on the
// request's type field and composes their result into a session. WebAuthn
// verification (goerp#301) isn't built yet; a request with type=webauthn
// is rejected the same way an unrecognized type is, until that lands.
package mfaverify

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/auth/authtoken"
	"github.com/djangbahevans/goerp/internal/engine/auth/loginsession"
	"github.com/djangbahevans/goerp/internal/engine/auth/mfatoken"
	"github.com/djangbahevans/goerp/internal/engine/cache"
	"github.com/djangbahevans/goerp/internal/engine/mfa"
	"github.com/djangbahevans/goerp/internal/engine/mfa/recoverycode"
	"github.com/djangbahevans/goerp/internal/engine/mfa/totp"
	"github.com/djangbahevans/goerp/internal/engine/tenant"
)

// maxBodyBytes bounds the request body before JSON parsing — no shared
// config field or middleware covers builtin routes yet (buildDispatchHandler
// runs no middleware ahead of them), same reasoning as loginflow's own cap.
const maxBodyBytes = 64 * 1024

// maxAttempts/lockoutWindow are auth-internals.md §8's own "5 consecutive
// failures ... lock MFA for 15 minutes" numbers — deliberately a
// different threshold and window from loginflow's password brute-force
// lockout (10 failures/30 min), since they guard different attack
// surfaces (password guessing vs. MFA code guessing once the password is
// already known).
const (
	maxAttempts   = 5
	lockoutWindow = 15 * time.Minute
)

type Handler struct {
	mfaTokens *mfatoken.Codec
	cache     *cache.Client
	totp      *totp.Service
	recovery  *recoverycode.Service
	tenants   *tenant.Store
	issuer    *authtoken.Issuer
}

func NewHandler(mfaTokens *mfatoken.Codec, cacheClient *cache.Client, totpService *totp.Service, recoveryService *recoverycode.Service, tenants *tenant.Store, issuer *authtoken.Issuer) *Handler {
	return &Handler{
		mfaTokens: mfaTokens,
		cache:     cacheClient,
		totp:      totpService,
		recovery:  recoveryService,
		tenants:   tenants,
		issuer:    issuer,
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

func writeJSONError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"error": map[string]string{"code": code, "message": message},
	})
}

func writeInvalidToken(w http.ResponseWriter) {
	writeJSONError(w, http.StatusUnauthorized, "invalid_mfa_token", "invalid, expired, or already-used mfa_token")
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	var req verifyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid_request", "malformed request body")
		return
	}

	ctx := r.Context()

	// Steps 1-2: validate HMAC signature + expiry, extract claims.
	claims, err := h.mfaTokens.Verify(req.MFAToken)
	if err != nil {
		writeInvalidToken(w)
		return
	}

	// Step 3: Origin check — rejects a captured token being submitted
	// from a different origin than the one that requested it.
	if r.Header.Get("Origin") != claims.Origin {
		writeInvalidToken(w)
		return
	}

	// Step 4: atomic single-use consumption. TTL matches the token's own
	// remaining life, not a fixed window — the consumption marker only
	// needs to outlive the token it's guarding.
	remaining := time.Until(claims.ExpiresAt.Time)
	if remaining <= 0 {
		writeInvalidToken(w)
		return
	}
	claimed, err := h.cache.SetNXWithTTL(ctx, consumedKey(claims.Txn), "1", remaining)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "internal_error", "mfa verification failed")
		return
	}
	if !claimed {
		writeInvalidToken(w)
		return
	}

	// Step 5: lockout check, before spending effort verifying the code.
	attempts, _, err := h.cache.Get(ctx, attemptsKey(claims.Subject, claims.TenantID))
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "internal_error", "mfa verification failed")
		return
	}
	if attempts == maxAttemptsMarker {
		writeJSONError(w, http.StatusLocked, "mfa_locked", "too many failed MFA attempts; try again later")
		return
	}

	// Step 6: verify the submitted code against an enrolled factor of the
	// requested type.
	valid, credentialID, err := h.verifyCode(ctx, claims.Subject, req.Type, req.Code)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "internal_error", "mfa verification failed")
		return
	}
	if !valid {
		// Step 8: on failure, increment the (user_id, tenant_id) attempt
		// counter — never the already-consumed txn from step 4.
		count, incErr := h.cache.IncrWithTTL(ctx, attemptsKey(claims.Subject, claims.TenantID), lockoutWindow)
		if incErr != nil {
			writeJSONError(w, http.StatusInternalServerError, "internal_error", "mfa verification failed")
			return
		}
		if count >= maxAttempts {
			if setErr := h.cache.SetWithTTL(ctx, attemptsKey(claims.Subject, claims.TenantID), maxAttemptsMarker, lockoutWindow); setErr != nil {
				writeJSONError(w, http.StatusInternalServerError, "internal_error", "mfa verification failed")
				return
			}
		}
		writeJSONError(w, http.StatusUnauthorized, "invalid_mfa_code", "invalid MFA code")
		return
	}

	// Step 7: full session issuance, with MFA assurance columns set. A
	// recovery-code Verify above already consumed that specific row
	// (mfa.Store.ConsumeOnce, called from inside recoverycode.Service) —
	// not literally in the same database transaction as the session
	// insert below, since they're separate stores with no shared *sql.Tx
	// today. If Issue fails here, the code is already spent and the
	// caller must retry with a different enrolled factor; this is a
	// narrower guarantee than a single atomic transaction would give, but
	// the security property that matters — a recovery code can't be used
	// twice — still holds, since ConsumeOnce's own atomicity is what
	// prevents reuse, not the transaction boundary.
	t, err := h.tenants.GetByID(ctx, claims.TenantID)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "internal_error", "mfa verification failed")
		return
	}

	nonBrowser := loginsession.IsNonBrowser(r)
	deviceID, deviceIDIsFresh := loginsession.ResolveDeviceID(r, req.DeviceID, nonBrowser)
	now := time.Now()

	tokens, err := h.issuer.Issue(ctx, authtoken.LoginParams{
		UserID:          claims.Subject,
		TenantSlug:      t.Slug,
		DeviceID:        deviceID,
		UserAgent:       r.UserAgent(),
		IPAddress:       loginsession.ClientIP(r),
		MFAMethod:       req.Type,
		MFAVerifiedAt:   &now,
		MFACredentialID: credentialID,
	})
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "internal_error", "mfa verification failed")
		return
	}

	// Reset the attempt counter on success so a later, separate login
	// attempt doesn't inherit an in-progress failure count.
	if err := h.cache.Delete(ctx, attemptsKey(claims.Subject, claims.TenantID)); err != nil {
		writeJSONError(w, http.StatusInternalServerError, "internal_error", "mfa verification failed")
		return
	}

	loginsession.WriteResponse(w, tokens, deviceID, deviceIDIsFresh, nonBrowser)
}

// verifyCode dispatches to the Service matching req.Type. totp and
// recoverycode share the same (bool, credentialID, error) shape by
// design, so both branches compose identically; webauthn (goerp#301)
// isn't built yet.
func (h *Handler) verifyCode(ctx context.Context, userID, mfaType, code string) (valid bool, credentialID string, err error) {
	switch mfa.CredentialType(mfaType) {
	case mfa.CredentialTOTP:
		return h.totp.Verify(ctx, userID, code)
	case mfa.CredentialRecoveryCode:
		return h.recovery.Verify(ctx, userID, code)
	default:
		return false, "", nil
	}
}

// consumedKey matches auth-internals.md §8's own
// auth:mfa_token:consumed:{txn} format exactly.
func consumedKey(txn string) string {
	return "auth:mfa_token:consumed:" + txn
}

// attemptsKey scopes the MFA lockout counter to (user_id, tenant_id), per
// auth-internals.md §8's "Attempt counter scope" — never to txn, since a
// per-transaction counter would reset to zero on every fresh
// POST /auth/login.
func attemptsKey(userID, tenantID string) string {
	return "auth:mfa_attempts:" + userID + ":" + tenantID
}

// maxAttemptsMarker is the sentinel value attemptsKey is set to once the
// lockout threshold is reached — distinguishing "locked" from "still
// counting" without a second Redis key.
const maxAttemptsMarker = "locked"
