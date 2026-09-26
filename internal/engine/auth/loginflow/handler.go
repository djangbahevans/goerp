// Package loginflow implements POST /auth/login — auth-internals.md §3
// "Login flow"'s documented 11-step order: email normalization, user
// lookup, status check, tenant membership check, brute-force check,
// Argon2id verification, MFA gating, and token issuance for both browser
// (cookie) and non-browser (JSON body) clients. A browser signing in on a
// host that doesn't resolve to the tenant gets a handoff code instead of
// a session, and POST /auth/handoff (ServeHandoff) on the tenant's own
// host exchanges it (auth-internals.md §3 "Shared-domain handoff").
//
// Before the user lookup, three Redis sliding-window limiters (auth-
// internals.md §15 "Login rate limiting") delay the attempt, reject it, or
// record a per-tenant flood in the auth audit log.
//
// Out of scope, left to the tickets that own them: credential-stuffing
// detection (backlog #287), and the full escalating account-lockout policy — doubling duration, security
// notification email, audit log entry, admin manual-unlock (backlog
// #291). This handler implements only the single-tier lockout
// user.Store.IncrementFailedLogins already provides, enough for step 5
// ("check brute force counters — reject if locked") to be real.
package loginflow

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/rs/zerolog/log"

	"github.com/djangbahevans/goerp/internal/engine/auth/authtoken"
	"github.com/djangbahevans/goerp/internal/engine/auth/handoff"
	"github.com/djangbahevans/goerp/internal/engine/auth/loginsession"
	"github.com/djangbahevans/goerp/internal/engine/auth/mfatoken"
	"github.com/djangbahevans/goerp/internal/engine/auth/password"
	"github.com/djangbahevans/goerp/internal/engine/authaudit"
	"github.com/djangbahevans/goerp/internal/engine/cache"
	"github.com/djangbahevans/goerp/internal/engine/mfa"
	"github.com/djangbahevans/goerp/internal/engine/role"
	"github.com/djangbahevans/goerp/internal/engine/tenant"
	tenantresolve "github.com/djangbahevans/goerp/internal/engine/tenant/resolve"
	"github.com/djangbahevans/goerp/internal/engine/user"
)

// maxBodyBytes bounds the request body before JSON parsing — no shared
// config field or middleware covers builtin routes yet (buildDispatchHandler
// runs no middleware ahead of them), so this handler applies its own cap
// rather than trusting an unbounded read.
const maxBodyBytes = 64 * 1024

// minResponseTime is the floor auth-internals.md §15 "Timing attack
// prevention" holds every response to, so a caller can't distinguish
// "no such user" from "wrong password" by response latency.
const minResponseTime = 300 * time.Millisecond

// Login rate limits (auth-internals.md §15 "Login rate limiting"). Over the
// per-IP limit delays the response, over the per-email limit rejects the
// attempt with 429, and over the per-tenant limit only writes an audit
// event.
const (
	ipLimit          = 20
	ipWindow         = 15 * time.Minute
	ipOverLimitDelay = 500 * time.Millisecond

	emailLimit  = 10
	emailWindow = 15 * time.Minute

	tenantLimit  = 100
	tenantWindow = time.Minute
)

type Handler struct {
	users     *user.Store
	tenants   *tenant.Store
	roles     *role.Store
	mfa       *mfa.Store
	issuer    *authtoken.Issuer
	mfaTokens *mfatoken.Codec
	policies  *password.PolicyStore
	hasher    *password.Hasher
	cache     *cache.Client
	audit     *authaudit.Store
	resolver  *tenantresolve.Resolver
	handoffs  *handoff.Store
}

func NewHandler(users *user.Store, tenants *tenant.Store, roles *role.Store, mfaStore *mfa.Store, issuer *authtoken.Issuer, mfaTokens *mfatoken.Codec, policies *password.PolicyStore, hasher *password.Hasher, cacheClient *cache.Client, audit *authaudit.Store, resolver *tenantresolve.Resolver, handoffs *handoff.Store) *Handler {
	return &Handler{users: users, tenants: tenants, roles: roles, mfa: mfaStore, issuer: issuer, mfaTokens: mfaTokens, policies: policies, hasher: hasher, cache: cacheClient, audit: audit, resolver: resolver, handoffs: handoffs}
}

type loginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
	Tenant   string `json:"tenant"`
	DeviceID string `json:"device_id"`
	// Remember is the browser login's "remember this device" choice. A
	// non-browser client's session is always persistent.
	Remember bool `json:"remember"`
}

// writeJSON matches encoding/json v1's Encoder defaults, which
// json.MarshalWrite doesn't apply on its own: '<', '>', '&' escaped for
// safe HTML embedding, U+2028/U+2029 escaped for safe JS embedding, and
// map keys sorted, so identical responses are byte-identical.
func writeJSON(w http.ResponseWriter, v any) {
	_ = json.MarshalWrite(w, v, jsontext.EscapeForHTML(true), jsontext.EscapeForJS(true), json.Deterministic(true))
}

func writeJSONError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	writeJSON(w, map[string]any{
		"error": map[string]string{"code": code, "message": message},
	})
}

func writeInvalidCredentials(w http.ResponseWriter) {
	writeJSONError(w, http.StatusUnauthorized, "invalid_credentials", "invalid email or password")
}

func writeOverloaded(w http.ResponseWriter) {
	w.Header().Set("Retry-After", strconv.Itoa(password.OverloadRetryAfterSeconds))
	writeJSONError(w, http.StatusServiceUnavailable, "overloaded", "too many sign-in attempts in progress, retry shortly")
}

func writeRateLimited(w http.ResponseWriter, retryAfter time.Duration) {
	w.Header().Set("Retry-After", strconv.Itoa(max(1, int(math.Ceil(retryAfter.Seconds())))))
	writeJSONError(w, http.StatusTooManyRequests, "rate_limit_exceeded", "too many sign-in attempts, retry later")
}

// emailKey hashes the lowercased email, so the per-email key has a fixed
// size and Redis never holds the address itself.
func emailKey(email string) string {
	sum := sha256.Sum256([]byte(email))
	return "ratelimit:login:email:" + hex.EncodeToString(sum[:])
}

// allow records one attempt against key. A Redis failure fails open,
// matching the engine-wide route limiter.
func (h *Handler) allow(ctx context.Context, limiter, key string, limit int, window time.Duration) (bool, time.Duration) {
	allowed, retryAfter, err := h.cache.SlidingWindowAllow(ctx, key, limit, window)
	if err != nil {
		log.Warn().Err(err).Str("limiter", limiter).Msg("loginflow: login rate limit check failed, failing open")
		return true, 0
	}
	return allowed, retryAfter
}

// checkClientLimits applies the per-IP limiter, which only delays, then
// the per-email limiter, reporting false once it has written a 429.
func (h *Handler) checkClientLimits(w http.ResponseWriter, r *http.Request, email string) bool {
	ctx := r.Context()
	if ok, _ := h.allow(ctx, "ip", "ratelimit:login:ip:"+loginsession.ClientIP(r), ipLimit, ipWindow); !ok {
		select {
		case <-time.After(ipOverLimitDelay):
		case <-ctx.Done():
		}
	}
	if ok, retryAfter := h.allow(ctx, "email", emailKey(email), emailLimit, emailWindow); !ok {
		writeRateLimited(w, retryAfter)
		return false
	}
	return true
}

// tenantRateExceededMetadata is login.tenant_rate_exceeded's audit
// metadata (auth-internals.md §15 "Login rate limiting").
var tenantRateExceededMetadata = fmt.Appendf(nil, `{"limit":%d,"window_seconds":%d}`, tenantLimit, int(tenantWindow.Seconds()))

// detectionTimeout bounds detectTenantFlood's Redis and audit writes, which
// run detached from the request so a client that disconnects can't
// suppress detection.
const detectionTimeout = 2 * time.Second

// detectTenantFlood records the attempt in the per-tenant window. Over the
// limit it logs a warning and writes a login.tenant_rate_exceeded audit
// event, at most once per tenant per window; the attempt proceeds either
// way.
func (h *Handler) detectTenantFlood(ctx context.Context, tenantID string) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), detectionTimeout)
	defer cancel()

	if ok, _ := h.allow(ctx, "tenant", "ratelimit:login:tenant:"+tenantID, tenantLimit, tenantWindow); ok {
		return
	}
	guardKey := "ratelimit:login:tenant_alerted:" + tenantID
	first, err := h.cache.SetNXWithTTL(ctx, guardKey, "1", tenantWindow)
	if err != nil {
		log.Warn().Err(err).Str("tenant_id", tenantID).Msg("loginflow: per-tenant login rate exceeded; alert guard failed, audit event skipped")
		return
	}
	if !first {
		return
	}
	log.Warn().Str("tenant_id", tenantID).Msg("loginflow: per-tenant login rate exceeded")
	if h.audit == nil {
		return
	}
	row := authaudit.Row{EventType: "login.tenant_rate_exceeded", TenantID: tenantID, Success: true, Metadata: tenantRateExceededMetadata}
	if err := h.audit.Insert(ctx, row); err != nil {
		log.Error().Err(err).Str("tenant_id", tenantID).Msg("loginflow: write login.tenant_rate_exceeded audit event")
		// Released so the next over-limit attempt retries the event.
		if err := h.cache.Delete(ctx, guardKey); err != nil {
			log.Warn().Err(err).Str("tenant_id", tenantID).Msg("loginflow: release per-tenant alert guard")
		}
	}
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
	if err := json.UnmarshalRead(r.Body, &req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid_request", "malformed request body")
		return
	}

	ctx := r.Context()
	email := strings.ToLower(req.Email)

	if !h.checkClientLimits(w, r, email) {
		return
	}

	// Looked up here for the per-tenant limiter's key. An unknown tenant
	// skips that limiter, which never changes the response, and is
	// rejected at step 4, after the user lookup.
	t, tenantErr := h.tenants.GetBySlug(ctx, req.Tenant)
	if tenantErr != nil && !errors.Is(tenantErr, tenant.ErrTenantNotFound) {
		writeJSONError(w, http.StatusInternalServerError, "internal_error", "login failed")
		return
	}
	if tenantErr == nil {
		h.detectTenantFlood(ctx, t.ID)
	}

	// Taken before the user lookup, so an overloaded 503 looks the same
	// whether or not the email exists (auth-internals.md §15).
	slot, err := h.hasher.Acquire(ctx)
	if err != nil {
		writeOverloaded(w)
		return
	}
	defer slot.Release()

	// Step 2/3: look up user, check status. "invited" (no password ever
	// set) and "not found" are deliberately the same code path — see
	// auth-internals.md §15 "Timing attack prevention".
	u, err := h.users.GetByEmail(ctx, email)
	if err != nil {
		if errors.Is(err, user.ErrUserNotFound) {
			slot.VerifyDummy(req.Password)
			writeInvalidCredentials(w)
			return
		}
		writeJSONError(w, http.StatusInternalServerError, "internal_error", "login failed")
		return
	}
	if u.PasswordHash == nil {
		slot.VerifyDummy(req.Password)
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

	// Step 4: tenant membership. The GetBySlug lookup above is required,
	// not just a business-logic existence check: role.Store.IsMember
	// interpolates the slug into a schema-qualified query via
	// tenantschema.Name, which is documented safe only because a slug
	// reaching it has already passed system.tenants' own CHECK-constrained
	// format — a guarantee that holds for req.Tenant only once it's
	// round-tripped through a real tenant row lookup, not for the raw,
	// unvalidated request field.
	if tenantErr != nil {
		writeInvalidCredentials(w)
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
	match, needsRehash, err := slot.Verify(req.Password, *u.PasswordHash)
	var newHash string
	var rehashErr error
	if err == nil && needsRehash {
		newHash, rehashErr = slot.Hash(req.Password)
	}
	// Nothing below hashes; the slot guards memory, not database latency.
	slot.Release()
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
	if needsRehash && rehashErr == nil {
		// A re-hash failure or update failure here doesn't fail the
		// login — the password was already verified correct; the
		// stored hash simply stays on its old (still valid) params
		// until the next successful login retries the upgrade.
		_ = h.users.UpdatePasswordHash(ctx, u.ID, newHash)
	}

	// auth-internals.md §3 "Password policy versioning": a nudge only,
	// never a reason to refuse the login.
	var updateRecommended bool
	if _, policyVersion, err := h.policies.Effective(ctx, t.ID); err != nil {
		log.Warn().Err(err).Str("tenant_id", t.ID).Msg("loginflow: password policy version lookup failed")
	} else {
		updateRecommended = password.UpdateRecommended(u.PasswordSetAtPolicyTenantID, u.PasswordSetAtPolicyVersion, t.ID, policyVersion)
	}

	// Step 10, shared-domain host: the session is issued on the tenant's
	// own host instead (auth-internals.md §3 "Shared-domain handoff").
	if h.handoffs.Needed(ctx, r, t.ID) {
		resp, err := h.handoffs.Issue(ctx, handoff.Grant{
			UserID:                    u.ID,
			TenantID:                  t.ID,
			Remember:                  req.Remember,
			PasswordUpdateRecommended: updateRecommended,
		}, t.Slug)
		if err != nil {
			log.Error().Err(err).Str("user_id", u.ID).Msg("loginflow: issue handoff")
			writeJSONError(w, http.StatusInternalServerError, "internal_error", "login failed")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		writeJSON(w, map[string]any{"handoff": resp})
		return
	}

	h.completeLogin(w, r, u.ID, t.ID, t.Slug, req.DeviceID, req.Remember, updateRecommended)
}

// completeLogin is login steps 10-11: an mfa_required challenge when the
// user has a factor enrolled, otherwise the session.
func (h *Handler) completeLogin(w http.ResponseWriter, r *http.Request, userID, tenantID, tenantSlug, bodyDeviceID string, remember, updateRecommended bool) {
	ctx := r.Context()

	// Step 10: MFA gating. Whether MFA is enrolled is the only signal
	// available today — the per-tenant enforcement-mode policy
	// (optional/required/required_for_roles, goerp#308) doesn't exist
	// yet, so any enrolled factor is treated as required.
	factors, err := h.mfa.ListActiveByUser(ctx, userID)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "internal_error", "login failed")
		return
	}
	if len(factors) > 0 {
		mfaToken, _, err := h.mfaTokens.Issue(userID, tenantID, r.Header.Get("Origin"), mfatoken.IssueOptions{
			Remember:                  remember,
			PasswordUpdateRecommended: updateRecommended,
		})
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, "internal_error", "login failed")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		writeJSON(w, map[string]any{
			"mfa_required": true,
			"mfa_token":    mfaToken,
			"mfa_methods":  enrolledMethods(factors),
		})
		return
	}

	// Step 11: full session issuance.
	nonBrowser := loginsession.IsNonBrowser(r)
	deviceID, deviceIDIsFresh := loginsession.ResolveDeviceID(r, bodyDeviceID, nonBrowser)

	tokens, err := h.issuer.Issue(ctx, authtoken.LoginParams{
		UserID:      userID,
		TenantSlug:  tenantSlug,
		DeviceID:    deviceID,
		UserAgent:   r.UserAgent(),
		IPAddress:   loginsession.ClientIP(r),
		CountryCode: "",
		Persistent:  nonBrowser || remember,
	})
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "internal_error", "login failed")
		return
	}
	if err := h.users.ResetLoginState(ctx, userID, loginsession.ClientIP(r)); err != nil {
		writeJSONError(w, http.StatusInternalServerError, "internal_error", "login failed")
		return
	}

	loginsession.WriteResponse(w, tokens, deviceID, deviceIDIsFresh, nonBrowser, updateRecommended)
}

type handoffRequest struct {
	Code string `json:"code"`
}

func writeHandoffInvalid(w http.ResponseWriter) {
	writeJSONError(w, http.StatusUnauthorized, "auth.handoff_code_invalid", "sign-in handoff expired or already used")
}

// ServeHandoff is POST /auth/handoff: on the tenant's own host, it
// exchanges a handoff code for login steps 10-11 (auth-internals.md §3
// "Shared-domain handoff").
func (h *Handler) ServeHandoff(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	tc, err := h.resolver.ResolveByHost(ctx, r.Host)
	if err != nil {
		switch {
		case errors.Is(err, tenantresolve.ErrTenantNotFound):
			writeJSONError(w, http.StatusNotFound, "not_found", "not found")
		case errors.Is(err, tenantresolve.ErrTenantSuspended):
			writeJSONError(w, http.StatusForbidden, "tenant_suspended", "tenant suspended")
		case errors.Is(err, tenantresolve.ErrTenantOffboarding):
			writeJSONError(w, http.StatusForbidden, "tenant_offboarding", "tenant offboarding")
		default:
			writeJSONError(w, http.StatusInternalServerError, "internal_error", "login failed")
		}
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	var req handoffRequest
	if err := json.UnmarshalRead(r.Body, &req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid_request", "malformed request body")
		return
	}

	grant, err := h.handoffs.Consume(ctx, req.Code)
	if errors.Is(err, handoff.ErrInvalidCode) {
		writeHandoffInvalid(w)
		return
	}
	if err != nil {
		log.Error().Err(err).Msg("loginflow: consume handoff code")
		writeJSONError(w, http.StatusInternalServerError, "internal_error", "login failed")
		return
	}
	if grant.TenantID != tc.TenantID {
		writeHandoffInvalid(w)
		return
	}

	// The grant is a snapshot of a login moments ago; the account may
	// have changed since.
	u, err := h.users.GetByID(ctx, grant.UserID)
	if errors.Is(err, user.ErrUserNotFound) {
		writeHandoffInvalid(w)
		return
	}
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "internal_error", "login failed")
		return
	}
	if u.Status != user.StatusActive {
		writeHandoffInvalid(w)
		return
	}
	isMember, err := h.roles.IsMember(ctx, tc.Slug, u.ID)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "internal_error", "login failed")
		return
	}
	if !isMember {
		writeHandoffInvalid(w)
		return
	}

	h.completeLogin(w, r, u.ID, tc.TenantID, tc.Slug, "", grant.Remember, grant.PasswordUpdateRecommended)
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
