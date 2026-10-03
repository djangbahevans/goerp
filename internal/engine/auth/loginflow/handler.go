// Package loginflow implements POST /auth/login — auth-internals.md §3
// "Login flow"'s documented 11-step order: email normalization, user
// lookup, status check, tenant membership check, brute-force check,
// Argon2id verification, MFA gating, and token issuance for both browser
// (cookie) and non-browser (JSON body) clients. A browser signing in on a
// host that doesn't resolve to the tenant gets a handoff code instead of
// a session, and POST /auth/handoff (ServeHandoff) on the tenant's own
// host exchanges it (auth-internals.md §3 "Shared-domain handoff").
//
// A login that names no tenant finds the account's tenants itself: one is
// signed in to directly, several answer tenant_required with a
// selection_token that POST /auth/select-tenant (ServeSelectTenant)
// exchanges for the chosen tenant (auth-internals.md §3 "Cross-tenant
// user membership").
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
	"github.com/djangbahevans/goerp/internal/engine/auth/ipallowlist"
	"github.com/djangbahevans/goerp/internal/engine/auth/loginsession"
	"github.com/djangbahevans/goerp/internal/engine/auth/membership"
	"github.com/djangbahevans/goerp/internal/engine/auth/mfatoken"
	"github.com/djangbahevans/goerp/internal/engine/auth/password"
	"github.com/djangbahevans/goerp/internal/engine/auth/tenantselect"
	"github.com/djangbahevans/goerp/internal/engine/authaudit"
	"github.com/djangbahevans/goerp/internal/engine/cache"
	"github.com/djangbahevans/goerp/internal/engine/httperr"
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
	users      *user.Store
	tenants    *tenant.Store
	roles      *role.Store
	mfa        *mfa.Store
	issuer     *authtoken.Issuer
	mfaTokens  *mfatoken.Codec
	policies   *password.PolicyStore
	hasher     *password.Hasher
	cache      *cache.Client
	audit      *authaudit.Store
	resolver   *tenantresolve.Resolver
	handoffs   *handoff.Store
	selections *tenantselect.Store
	allowlists *ipallowlist.Store
}

func NewHandler(users *user.Store, tenants *tenant.Store, roles *role.Store, mfaStore *mfa.Store, issuer *authtoken.Issuer, mfaTokens *mfatoken.Codec, policies *password.PolicyStore, hasher *password.Hasher, cacheClient *cache.Client, audit *authaudit.Store, resolver *tenantresolve.Resolver, handoffs *handoff.Store, selections *tenantselect.Store, allowlists *ipallowlist.Store) *Handler {
	return &Handler{users: users, tenants: tenants, roles: roles, mfa: mfaStore, issuer: issuer, mfaTokens: mfaTokens, policies: policies, hasher: hasher, cache: cacheClient, audit: audit, resolver: resolver, handoffs: handoffs, selections: selections, allowlists: allowlists}
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

func writeInvalidCredentials(w http.ResponseWriter, r *http.Request) {
	httperr.Write(r.Context(), w, http.StatusUnauthorized, "invalid_credentials", "invalid email or password")
}

func writeOverloaded(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Retry-After", strconv.Itoa(password.OverloadRetryAfterSeconds))
	httperr.Write(r.Context(), w, http.StatusServiceUnavailable, "overloaded", "too many sign-in attempts in progress, retry shortly")
}

// allowedFrom reports whether tenantID's login IP allowlist admits r's
// client, having written the 403 (or, if the list can't be read, a 500)
// when it doesn't.
func (h *Handler) allowedFrom(w http.ResponseWriter, r *http.Request, tenantID string) bool {
	if h.allowlists == nil {
		return true
	}

	ip := loginsession.ClientIP(r)
	ok, err := h.allowlists.Check(r.Context(), tenantID, ip)
	if err != nil {
		log.Error().Err(err).Str("tenant_id", tenantID).Msg("loginflow: ip allowlist check failed")
		httperr.Write(r.Context(), w, http.StatusInternalServerError, "internal_error", "login failed")
		return false
	}

	if !ok {
		log.Info().Str("tenant_id", tenantID).Str("ip", ip).Msg("loginflow: sign-in from an address outside the tenant's ip allowlist")
		httperr.Write(r.Context(), w, http.StatusForbidden, "ip_not_allowed", "signing in to this tenant is not allowed from your network")
		return false
	}

	return true
}

func writeRateLimited(w http.ResponseWriter, r *http.Request, retryAfter time.Duration) {
	w.Header().Set("Retry-After", strconv.Itoa(max(1, int(math.Ceil(retryAfter.Seconds())))))
	httperr.Write(r.Context(), w, http.StatusTooManyRequests, "rate_limit_exceeded", "too many sign-in attempts, retry later")
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

// delayOverIPLimit applies the per-IP limiter, which only delays.
func (h *Handler) delayOverIPLimit(r *http.Request) {
	ctx := r.Context()
	if ok, _ := h.allow(ctx, "ip", "ratelimit:login:ip:"+loginsession.ClientIP(r), ipLimit, ipWindow); !ok {
		select {
		case <-time.After(ipOverLimitDelay):
		case <-ctx.Done():
		}
	}
}

// checkClientLimits applies the per-IP limiter, then the per-email
// limiter, reporting false once it has written a 429.
func (h *Handler) checkClientLimits(w http.ResponseWriter, r *http.Request, email string) bool {
	ctx := r.Context()
	h.delayOverIPLimit(r)
	if ok, retryAfter := h.allow(ctx, "email", emailKey(email), emailLimit, emailWindow); !ok {
		writeRateLimited(w, r, retryAfter)
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
		httperr.Write(r.Context(), w, http.StatusBadRequest, "invalid_request", "malformed request body")
		return
	}

	ctx := r.Context()
	email := strings.ToLower(req.Email)

	if !h.checkClientLimits(w, r, email) {
		return
	}

	// Looked up here for the per-tenant limiter's key. An unknown tenant
	// skips that limiter, which never changes the response, and is
	// rejected at step 4, after the user lookup. With no tenant named,
	// step 4 runs after the password check instead.
	tenantless := req.Tenant == ""
	var t *tenant.Tenant
	var tenantErr error
	if !tenantless {
		t, tenantErr = h.tenants.GetBySlug(ctx, req.Tenant)
		if tenantErr != nil && !errors.Is(tenantErr, tenant.ErrTenantNotFound) {
			httperr.Write(r.Context(), w, http.StatusInternalServerError, "internal_error", "login failed")
			return
		}

		if tenantErr == nil {
			h.detectTenantFlood(ctx, t.ID)
		}
	}

	// Taken before the user lookup, so an overloaded 503 looks the same
	// whether or not the email exists (auth-internals.md §15).
	slot, err := h.hasher.Acquire(ctx)
	if err != nil {
		writeOverloaded(w, r)
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
			writeInvalidCredentials(w, r)
			return
		}

		httperr.Write(r.Context(), w, http.StatusInternalServerError, "internal_error", "login failed")
		return
	}

	if u.PasswordHash == nil {
		slot.VerifyDummy(req.Password)
		writeInvalidCredentials(w, r)
		return
	}

	switch u.Status {
	case user.StatusSuspended, user.StatusDeleted:
		writeInvalidCredentials(w, r)
		return
	case user.StatusPendingVerification:
		httperr.Write(r.Context(), w, http.StatusForbidden, "email_verification_required", "email verification is required before login")
		return
	case user.StatusActive:
		// proceeds below
	default:
		writeInvalidCredentials(w, r)
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
	if !tenantless {
		if tenantErr != nil {
			writeInvalidCredentials(w, r)
			return
		}

		isMember, err := h.roles.IsMember(ctx, req.Tenant, u.ID)
		if err != nil {
			httperr.Write(r.Context(), w, http.StatusInternalServerError, "internal_error", "login failed")
			return
		}

		if !isMember {
			writeInvalidCredentials(w, r)
			return
		}
	}

	// Step 5: brute-force check — rejected exactly like a wrong password,
	// never a distinct response, per auth-internals.md §15 "Account
	// lockout" ("don't confirm lockout to the attacker").
	if u.LockedUntil != nil && u.LockedUntil.After(time.Now()) {
		writeInvalidCredentials(w, r)
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
		httperr.Write(r.Context(), w, http.StatusInternalServerError, "internal_error", "login failed")
		return
	}

	if !match {
		if incErr := h.users.IncrementFailedLogins(ctx, u.ID); incErr != nil {
			httperr.Write(r.Context(), w, http.StatusInternalServerError, "internal_error", "login failed")
			return
		}

		writeInvalidCredentials(w, r)
		return
	}

	if needsRehash && rehashErr == nil {
		// A re-hash failure or update failure here doesn't fail the
		// login — the password was already verified correct; the
		// stored hash simply stays on its old (still valid) params
		// until the next successful login retries the upgrade.
		_ = h.users.UpdatePasswordHash(ctx, u.ID, newHash)
	}

	// Step 4 for a tenantless login (auth-internals.md §3 "Cross-tenant
	// user membership"): one tenant is inferred, several need a pick.
	if tenantless {
		memberships, err := membership.TenantsOf(ctx, h.tenants, h.roles, u.ID)
		if err != nil {
			log.Error().Err(err).Str("user_id", u.ID).Msg("loginflow: list tenant memberships")
			httperr.Write(r.Context(), w, http.StatusInternalServerError, "internal_error", "login failed")
			return
		}

		switch len(memberships) {
		case 0:
			writeInvalidCredentials(w, r)
			return
		case 1:
			t = &memberships[0]
		default:
			// Step 8a for each tenant the user may pick: the pick that
			// follows no longer has the password.
			results := make(map[string]password.Result, len(memberships))
			for _, m := range memberships {
				results[m.ID] = h.policies.CheckSignIn(ctx, m.ID, m.Slug, u.ID, req.Password, u.Email)
			}

			h.writeTenantRequired(w, r, u.ID, memberships, results, req)
			return
		}
	}

	// Step 8a: check the password against this tenant's current rules
	// (auth-internals.md §3 "Password policy at sign-in"). Never a reason
	// to refuse the login; the result is applied when the session is
	// issued.
	policy := h.policies.CheckSignIn(ctx, t.ID, t.Slug, u.ID, req.Password, u.Email)

	h.signIn(w, r, u, t, req.DeviceID, req.Remember, policy)
}

type tenantChoice struct {
	Slug string `json:"slug"`
	Name string `json:"name"`
}

// writeTenantRequired answers a tenantless login whose account belongs to
// several tenants with a selection_token for POST /auth/select-tenant.
func (h *Handler) writeTenantRequired(w http.ResponseWriter, r *http.Request, userID string, memberships []tenant.Tenant, results map[string]password.Result, req loginRequest) {
	token, err := h.selections.Issue(r.Context(), tenantselect.Grant{UserID: userID, Remember: req.Remember, DeviceID: req.DeviceID, PasswordPolicyResults: results})
	if err != nil {
		log.Error().Err(err).Str("user_id", userID).Msg("loginflow: issue selection token")
		httperr.Write(r.Context(), w, http.StatusInternalServerError, "internal_error", "login failed")
		return
	}

	choices := make([]tenantChoice, len(memberships))
	for i, t := range memberships {
		choices[i] = tenantChoice{Slug: t.Slug, Name: t.Name}
	}

	httperr.WriteDetails(r.Context(), w, http.StatusConflict, "tenant_required", "choose a tenant to sign in to", map[string]any{"tenants": choices, "selection_token": token})
}

// signIn is login step 10 onward for a verified user signing in to t: a
// handoff on another host, otherwise completeLogin. policy is step 8a's
// result for t.
func (h *Handler) signIn(w http.ResponseWriter, r *http.Request, u *user.User, t *tenant.Tenant, deviceID string, remember bool, policy password.Result) {
	ctx := r.Context()

	// Only after the password check, so the response can't be used to
	// probe a tenant's allowlist without valid credentials.
	if !h.allowedFrom(w, r, t.ID) {
		return
	}

	// Step 10, shared-domain host: the session is issued on the tenant's
	// own host instead (auth-internals.md §3 "Shared-domain handoff").
	if h.handoffs.Needed(ctx, r, t.ID) {
		resp, err := h.handoffs.Issue(ctx, handoff.Grant{
			UserID:               u.ID,
			TenantID:             t.ID,
			Remember:             remember,
			PasswordPolicyResult: policy,
		}, t.Slug)
		if err != nil {
			log.Error().Err(err).Str("user_id", u.ID).Msg("loginflow: issue handoff")
			httperr.Write(r.Context(), w, http.StatusInternalServerError, "internal_error", "login failed")
			return
		}

		w.Header().Set("Content-Type", "application/json")
		writeJSON(w, map[string]any{"handoff": resp})
		return
	}

	h.completeLogin(w, r, u.ID, t.ID, t.Slug, deviceID, remember, policy)
}

type selectTenantRequest struct {
	SelectionToken string `json:"selection_token"`
	Tenant         string `json:"tenant"`
}

func writeSelectionTokenInvalid(w http.ResponseWriter, r *http.Request) {
	httperr.Write(r.Context(), w, http.StatusUnauthorized, "auth.selection_token_invalid", "tenant selection expired or already used")
}

// ServeSelectTenant is POST /auth/select-tenant: it finishes a tenantless
// login for the tenant the user picked, against the selection_token that
// login answered with (auth-internals.md §3 "Cross-tenant user
// membership").
func (h *Handler) ServeSelectTenant(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	defer func() {
		if elapsed := time.Since(start); elapsed < minResponseTime {
			time.Sleep(minResponseTime - elapsed)
		}
	}()

	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	var req selectTenantRequest
	if err := json.UnmarshalRead(r.Body, &req); err != nil {
		httperr.Write(r.Context(), w, http.StatusBadRequest, "invalid_request", "malformed request body")
		return
	}

	ctx := r.Context()
	h.delayOverIPLimit(r)

	grant, err := h.selections.Consume(ctx, req.SelectionToken)
	if errors.Is(err, tenantselect.ErrInvalidToken) {
		writeSelectionTokenInvalid(w, r)
		return
	}

	if err != nil {
		log.Error().Err(err).Msg("loginflow: consume selection token")
		httperr.Write(r.Context(), w, http.StatusInternalServerError, "internal_error", "login failed")
		return
	}

	// The grant is a snapshot of a login moments ago; the account and its
	// memberships may have changed since.
	u, err := h.users.GetByID(ctx, grant.UserID)
	if errors.Is(err, user.ErrUserNotFound) {
		writeSelectionTokenInvalid(w, r)
		return
	}

	if err != nil {
		httperr.Write(r.Context(), w, http.StatusInternalServerError, "internal_error", "login failed")
		return
	}

	if u.Status != user.StatusActive {
		writeSelectionTokenInvalid(w, r)
		return
	}

	// GetBySlug before IsMember: IsMember interpolates the slug into a
	// schema name, safe only for a slug read back from a real row.
	t, err := h.tenants.GetBySlug(ctx, req.Tenant)
	if errors.Is(err, tenant.ErrTenantNotFound) {
		httperr.Write(r.Context(), w, http.StatusForbidden, "tenant_membership_required", "not a member of this tenant")
		return
	}

	if err != nil {
		httperr.Write(r.Context(), w, http.StatusInternalServerError, "internal_error", "login failed")
		return
	}

	isMember, err := h.roles.IsMember(ctx, t.Slug, u.ID)
	if err != nil {
		httperr.Write(r.Context(), w, http.StatusInternalServerError, "internal_error", "login failed")
		return
	}

	if !isMember {
		httperr.Write(r.Context(), w, http.StatusForbidden, "tenant_membership_required", "not a member of this tenant")
		return
	}

	// Only a listed tenant has a sign-in password check result; one joined
	// since the login would otherwise skip it.
	policy, listed := grant.PasswordPolicyResults[t.ID]
	if !listed {
		httperr.Write(r.Context(), w, http.StatusForbidden, "tenant_membership_required", "not a member of this tenant")
		return
	}

	h.signIn(w, r, u, t, grant.DeviceID, grant.Remember, policy)
}

func (h *Handler) completeLogin(w http.ResponseWriter, r *http.Request, userID, tenantID, tenantSlug, bodyDeviceID string, remember bool, policy password.Result) {
	ctx := r.Context()

	factors, err := h.mfa.ListAccepted(ctx, userID, mfa.Scope{TenantID: tenantID})
	if err != nil {
		httperr.Write(r.Context(), w, http.StatusInternalServerError, "internal_error", "login failed")
		return
	}

	if mfa.HasFactor(factors) {
		mfaToken, _, err := h.mfaTokens.Issue(userID, tenantID, r.Header.Get("Origin"), mfatoken.IssueOptions{
			Remember:       remember,
			PasswordPolicy: policy,
		})
		if err != nil {
			httperr.Write(r.Context(), w, http.StatusInternalServerError, "internal_error", "login failed")
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

		PasswordChangeRequired: policy.Outcome == password.ChangeRequired,
	})
	if errors.Is(err, authtoken.ErrIPNotAllowed) {
		httperr.Write(r.Context(), w, http.StatusForbidden, "ip_not_allowed", "signing in to this tenant is not allowed from your network")
		return
	}

	if err != nil {
		httperr.Write(r.Context(), w, http.StatusInternalServerError, "internal_error", "login failed")
		return
	}

	if err := h.users.ResetLoginState(ctx, userID); err != nil {
		httperr.Write(r.Context(), w, http.StatusInternalServerError, "internal_error", "login failed")
		return
	}

	loginsession.WriteResponse(w, tokens, deviceID, deviceIDIsFresh, nonBrowser, policy)
}

type handoffRequest struct {
	Code string `json:"code"`
}

func writeHandoffInvalid(w http.ResponseWriter, r *http.Request) {
	httperr.Write(r.Context(), w, http.StatusUnauthorized, "auth.handoff_code_invalid", "sign-in handoff expired or already used")
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
			httperr.Write(r.Context(), w, http.StatusNotFound, "not_found", "not found")
		case errors.Is(err, tenantresolve.ErrTenantSuspended):
			httperr.Write(r.Context(), w, http.StatusForbidden, "tenant_suspended", "tenant suspended")
		case errors.Is(err, tenantresolve.ErrTenantOffboarding):
			httperr.Write(r.Context(), w, http.StatusForbidden, "tenant_offboarding", "tenant offboarding")
		default:
			httperr.Write(r.Context(), w, http.StatusInternalServerError, "internal_error", "login failed")
		}

		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	var req handoffRequest
	if err := json.UnmarshalRead(r.Body, &req); err != nil {
		httperr.Write(r.Context(), w, http.StatusBadRequest, "invalid_request", "malformed request body")
		return
	}

	grant, err := h.handoffs.Consume(ctx, req.Code)
	if errors.Is(err, handoff.ErrInvalidCode) {
		writeHandoffInvalid(w, r)
		return
	}

	if err != nil {
		log.Error().Err(err).Msg("loginflow: consume handoff code")
		httperr.Write(r.Context(), w, http.StatusInternalServerError, "internal_error", "login failed")
		return
	}

	if grant.TenantID != tc.TenantID {
		writeHandoffInvalid(w, r)
		return
	}

	// The grant is a snapshot of a login moments ago; the account may
	// have changed since.
	u, err := h.users.GetByID(ctx, grant.UserID)
	if errors.Is(err, user.ErrUserNotFound) {
		writeHandoffInvalid(w, r)
		return
	}

	if err != nil {
		httperr.Write(r.Context(), w, http.StatusInternalServerError, "internal_error", "login failed")
		return
	}

	if u.Status != user.StatusActive {
		writeHandoffInvalid(w, r)
		return
	}

	isMember, err := h.roles.IsMember(ctx, tc.Slug, u.ID)
	if err != nil {
		httperr.Write(r.Context(), w, http.StatusInternalServerError, "internal_error", "login failed")
		return
	}

	if !isMember {
		writeHandoffInvalid(w, r)
		return
	}

	// completeLogin's Issue re-checks the IP allowlist against this
	// request, which can come from another network than the login did.
	h.completeLogin(w, r, u.ID, tc.TenantID, tc.Slug, "", grant.Remember, grant.PasswordPolicyResult)
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
