// Package loginflow implements POST /auth/login — auth-internals.md §3
// "Login flow"'s documented 11-step order: email normalization, user
// lookup, status check, tenant membership check, brute-force check,
// Argon2id verification, MFA gating, and token issuance for both browser
// (cookie) and non-browser (JSON body) clients.
//
// Out of scope, left to the tickets that own them: the global Argon2
// concurrency semaphore (backlog #286), the three partitioned Redis rate
// limiters and credential-stuffing detection (backlog #287), and the
// full escalating account-lockout policy — doubling duration, security
// notification email, audit log entry, admin manual-unlock (backlog
// #291). This handler implements only the single-tier lockout
// user.Store.IncrementFailedLogins already provides, enough for step 5
// ("check brute force counters — reject if locked") to be real.
package loginflow

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/alexedwards/argon2id"

	"github.com/djangbahevans/goerp/internal/engine/auth/authtoken"
	"github.com/djangbahevans/goerp/internal/engine/auth/loginsession"
	"github.com/djangbahevans/goerp/internal/engine/auth/mfatoken"
	"github.com/djangbahevans/goerp/internal/engine/mfa"
	"github.com/djangbahevans/goerp/internal/engine/role"
	"github.com/djangbahevans/goerp/internal/engine/tenant"
	"github.com/djangbahevans/goerp/internal/engine/user"
)

// argonParams matches auth-internals.md §3 "Hashing" exactly.
var argonParams = &argon2id.Params{
	Memory:      64 * 1024,
	Iterations:  3,
	Parallelism: 4,
	SaltLength:  16,
	KeyLength:   32,
}

// maxBodyBytes bounds the request body before JSON parsing — no shared
// config field or middleware covers builtin routes yet (buildDispatchHandler
// runs no middleware ahead of them), so this handler applies its own cap
// rather than trusting an unbounded read.
const maxBodyBytes = 64 * 1024

// minResponseTime is the floor auth-internals.md §15 "Timing attack
// prevention" holds every response to, so a caller can't distinguish
// "no such user" from "wrong password" by response latency.
const minResponseTime = 300 * time.Millisecond

var dummyHash string

func init() {
	h, err := argon2id.CreateHash("timing-normalisation-dummy-password", argonParams)
	if err != nil {
		// argonParams is a fixed literal — CreateHash can only fail here
		// from a coding mistake in that literal, never from runtime
		// conditions, so failing loudly at startup is correct.
		panic(fmt.Sprintf("loginflow: create dummy hash: %v", err))
	}
	dummyHash = h
}

type Handler struct {
	users     *user.Store
	tenants   *tenant.Store
	roles     *role.Store
	mfa       *mfa.Store
	issuer    *authtoken.Issuer
	mfaTokens *mfatoken.Codec
}

func NewHandler(users *user.Store, tenants *tenant.Store, roles *role.Store, mfaStore *mfa.Store, issuer *authtoken.Issuer, mfaTokens *mfatoken.Codec) *Handler {
	return &Handler{users: users, tenants: tenants, roles: roles, mfa: mfaStore, issuer: issuer, mfaTokens: mfaTokens}
}

type loginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
	Tenant   string `json:"tenant"`
	DeviceID string `json:"device_id"`
}

func writeJSONError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"error": map[string]string{"code": code, "message": message},
	})
}

func writeInvalidCredentials(w http.ResponseWriter) {
	writeJSONError(w, http.StatusUnauthorized, "invalid_credentials", "invalid email or password")
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	defer func() {
		if elapsed := time.Since(start); elapsed < minResponseTime {
			time.Sleep(minResponseTime - elapsed)
		}
	}()

	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	var req loginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid_request", "malformed request body")
		return
	}

	ctx := r.Context()
	email := strings.ToLower(req.Email)

	// Step 2/3: look up user, check status. "invited" (no password ever
	// set) and "not found" are deliberately the same code path — see
	// auth-internals.md §15 "Timing attack prevention".
	u, err := h.users.GetByEmail(ctx, email)
	if err != nil {
		if errors.Is(err, user.ErrUserNotFound) {
			runDummyCompare(req.Password)
			writeInvalidCredentials(w)
			return
		}
		writeJSONError(w, http.StatusInternalServerError, "internal_error", "login failed")
		return
	}
	if u.PasswordHash == nil {
		runDummyCompare(req.Password)
		writeInvalidCredentials(w)
		return
	}
	switch u.Status {
	case user.StatusSuspended, user.StatusDeleted:
		writeInvalidCredentials(w)
		return
	case user.StatusPendingVerification:
		writeJSONError(w, http.StatusForbidden, "email_verification_required", "email verification is required before login")
		return
	case user.StatusActive:
		// proceeds below
	default:
		writeInvalidCredentials(w)
		return
	}

	// Step 4: tenant membership. GetBySlug's return value isn't used
	// directly, but the call itself is required, not just a business-logic
	// existence check: role.Store.IsMember interpolates the slug into a
	// schema-qualified query via tenantschema.Name, which is documented
	// safe only because a slug reaching it has already passed
	// system.tenants' own CHECK-constrained format — a guarantee that
	// holds for req.Tenant only once it's round-tripped through a real
	// tenant row lookup, not for the raw, unvalidated request field.
	t, err := h.tenants.GetBySlug(ctx, req.Tenant)
	if err != nil {
		if errors.Is(err, tenant.ErrTenantNotFound) {
			writeInvalidCredentials(w)
			return
		}
		writeJSONError(w, http.StatusInternalServerError, "internal_error", "login failed")
		return
	}
	isMember, err := h.roles.IsMember(ctx, req.Tenant, u.ID)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "internal_error", "login failed")
		return
	}
	if !isMember {
		writeInvalidCredentials(w)
		return
	}

	// Step 5: brute-force check — rejected exactly like a wrong password,
	// never a distinct response, per auth-internals.md §15 "Account
	// lockout" ("don't confirm lockout to the attacker").
	if u.LockedUntil != nil && u.LockedUntil.After(time.Now()) {
		writeInvalidCredentials(w)
		return
	}

	// Steps 6-9: Argon2id verify, transparent re-hash if params outdated.
	match, params, err := argon2id.CheckHash(req.Password, *u.PasswordHash)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "internal_error", "login failed")
		return
	}
	if !match {
		if incErr := h.users.IncrementFailedLogins(ctx, u.ID); incErr != nil {
			writeJSONError(w, http.StatusInternalServerError, "internal_error", "login failed")
			return
		}
		writeInvalidCredentials(w)
		return
	}
	if !paramsMatch(params, argonParams) {
		newHash, hashErr := argon2id.CreateHash(req.Password, argonParams)
		if hashErr == nil {
			// A re-hash failure or update failure here doesn't fail the
			// login — the password was already verified correct; the
			// stored hash simply stays on its old (still valid) params
			// until the next successful login retries the upgrade.
			_ = h.users.UpdatePasswordHash(ctx, u.ID, newHash)
		}
	}

	// Step 10: MFA gating. Whether MFA is enrolled is the only signal
	// available today — the per-tenant enforcement-mode policy
	// (optional/required/required_for_roles, goerp#308) doesn't exist
	// yet, so any enrolled factor is treated as required.
	factors, err := h.mfa.ListActiveByUser(ctx, u.ID)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "internal_error", "login failed")
		return
	}
	if len(factors) > 0 {
		mfaToken, _, err := h.mfaTokens.Issue(u.ID, t.ID, r.Header.Get("Origin"))
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, "internal_error", "login failed")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"mfa_required": true,
			"mfa_token":    mfaToken,
			"mfa_methods":  enrolledMethods(factors),
		})
		return
	}

	// Step 11: full session issuance.
	nonBrowser := loginsession.IsNonBrowser(r)
	deviceID, deviceIDIsFresh := loginsession.ResolveDeviceID(r, req.DeviceID, nonBrowser)

	tokens, err := h.issuer.Issue(ctx, authtoken.LoginParams{
		UserID:      u.ID,
		TenantSlug:  req.Tenant,
		DeviceID:    deviceID,
		UserAgent:   r.UserAgent(),
		IPAddress:   loginsession.ClientIP(r),
		CountryCode: "",
	})
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "internal_error", "login failed")
		return
	}
	if err := h.users.ResetLoginState(ctx, u.ID, loginsession.ClientIP(r)); err != nil {
		writeJSONError(w, http.StatusInternalServerError, "internal_error", "login failed")
		return
	}

	loginsession.WriteResponse(w, tokens, deviceID, deviceIDIsFresh, nonBrowser)
}

// runDummyCompare performs one Argon2id comparison against a fixed,
// precomputed hash — the same cost as a real verification — so a
// not-found or never-had-a-password account can't be distinguished from
// a wrong-password rejection by response latency alone (minResponseTime
// is a coarser floor on top of this; both apply).
func runDummyCompare(password string) {
	_, _ = argon2id.ComparePasswordAndHash(password, dummyHash)
}

func paramsMatch(got, want *argon2id.Params) bool {
	return got.Memory == want.Memory &&
		got.Iterations == want.Iterations &&
		got.Parallelism == want.Parallelism &&
		got.SaltLength == want.SaltLength &&
		got.KeyLength == want.KeyLength
}

// enrolledMethods returns the distinct set of credential types among
// factors, in a fixed, deterministic order — the mfa_methods field
// auth-internals.md §8's mfa_required response sample documents, telling
// the client which `type` values POST /auth/mfa/verify will accept for
// this user.
func enrolledMethods(factors []*mfa.Credential) []string {
	seen := make(map[mfa.CredentialType]bool, len(factors))
	for _, f := range factors {
		seen[f.Type] = true
	}

	var methods []string
	for _, t := range []mfa.CredentialType{mfa.CredentialTOTP, mfa.CredentialWebAuthn, mfa.CredentialRecoveryCode} {
		if seen[t] {
			methods = append(methods, string(t))
		}
	}
	return methods
}
