// Package adminsettings implements the tenant settings API behind the
// shell's /admin/settings page (shell-ux.md §5.5): GET and PATCH
// /admin/settings for the General, Security and Localisation sections,
// and POST/DELETE /admin/settings/logo for the company logo. The Email
// section's settings are their own concern (goerp#1284/#1293), and
// allowed OAuth providers wait on OAuth login itself.
//
// The company profile lives in system.tenants' columns; everything else
// in tenantconfig, through the stores that enforce or serve it:
// enforce.Store (MFA enforcement), password.PolicyStore (password
// policy), sessionpolicy.Store (session idle timeout and absolute
// maximum), ipallowlist.Store (the login IP allowlist) and
// tenantl10n.Store (default locale and timezone, and Localisation). Each
// is read uncached on use, so a change applies from the very next request.
//
// Like internal/engine/auth/adminroles, these are Class A tenant-facing
// routes despite the "/admin/" prefix: Host-header tenant resolution,
// session authentication and the admin role in the resolved tenant.
package adminsettings

import (
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"net/http"
	"slices"

	"github.com/rs/zerolog/log"

	"github.com/djangbahevans/goerp/internal/engine/auth/authcheck"
	"github.com/djangbahevans/goerp/internal/engine/auth/ipallowlist"
	"github.com/djangbahevans/goerp/internal/engine/auth/loginsession"
	"github.com/djangbahevans/goerp/internal/engine/auth/password"
	"github.com/djangbahevans/goerp/internal/engine/auth/sessionpolicy"
	"github.com/djangbahevans/goerp/internal/engine/authaudit"
	"github.com/djangbahevans/goerp/internal/engine/cache"
	"github.com/djangbahevans/goerp/internal/engine/files"
	"github.com/djangbahevans/goerp/internal/engine/httperr"
	"github.com/djangbahevans/goerp/internal/engine/l10n/tenantl10n"
	"github.com/djangbahevans/goerp/internal/engine/mfa/enforce"
	"github.com/djangbahevans/goerp/internal/engine/role"
	"github.com/djangbahevans/goerp/internal/engine/storage"
	"github.com/djangbahevans/goerp/internal/engine/tenant"
	tenantresolve "github.com/djangbahevans/goerp/internal/engine/tenant/resolve"
	"github.com/djangbahevans/goerp/internal/engine/tenantconfig"
)

const (
	maxBodyBytes  = 64 * 1024
	adminRoleName = "admin"
)

// AuditRecorder is satisfied by authaudit.Store.
type AuditRecorder interface {
	Insert(ctx context.Context, row authaudit.Row) error
}

// Deps are the Handler's collaborators.
type Deps struct {
	Tenants      *tenantresolve.Resolver
	Auth         *authcheck.Checker
	TenantStore  *tenant.Store
	Cache        *cache.Client
	Roles        *role.Store
	Config       *tenantconfig.Store
	MFA          *enforce.Store
	Passwords    *password.PolicyStore
	Sessions     *sessionpolicy.Store
	IPAllowlists *ipallowlist.Store
	Locales      *tenantl10n.Store
	Audit        AuditRecorder

	// Storage and Files back the logo upload; Storage may be nil when no
	// backend is configured, which fails only the upload.
	Storage      storage.Backend
	Files        *files.Store
	MaxLogoBytes int64
}

type Handler struct {
	deps Deps
}

func NewHandler(deps Deps) *Handler {
	return &Handler{deps: deps}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.MarshalWrite(w, v, jsontext.EscapeForHTML(true), jsontext.EscapeForJS(true))
}

func writeInternalError(w http.ResponseWriter, r *http.Request, err error, msg string) {
	log.Error().Err(err).Msg("adminsettings: " + msg)
	httperr.Write(r.Context(), w, http.StatusInternalServerError, "internal_error", "request failed")
}

// fieldError is a request value that fails validation, reported as a 422
// naming the offending field (section.field).
type fieldError struct {
	field   string
	message string
}

func (e *fieldError) Error() string { return e.field + ": " + e.message }

func invalid(field, format string, args ...any) *fieldError {
	return &fieldError{field: field, message: fmt.Sprintf(format, args...)}
}

func writeFieldError(w http.ResponseWriter, r *http.Request, e *fieldError) {
	httperr.WriteDetails(r.Context(), w, http.StatusUnprocessableEntity, "invalid_setting", e.message, map[string]string{"field": e.field})
}

type caller struct {
	tenant *tenantresolve.TenantContext
	auth   *authcheck.AuthContext
}

func (h *Handler) authorize(w http.ResponseWriter, r *http.Request) (caller, bool) {
	ctx := r.Context()
	tenantCtx, err := h.deps.Tenants.ResolveByHost(ctx, r.Host)
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
		return caller{}, false
	}

	rawToken := authcheck.ExtractToken(r)
	if rawToken == "" {
		httperr.Write(r.Context(), w, http.StatusUnauthorized, "unauthenticated", "a valid access token is required")
		return caller{}, false
	}
	authCtx, err := h.deps.Auth.Authenticate(ctx, rawToken, tenantCtx.TenantID, tenantCtx.Slug, loginsession.ClientIP(r), nil, nil)
	if err != nil || !authCtx.IsAuthenticated {
		httperr.Write(r.Context(), w, http.StatusUnauthorized, "unauthenticated", "a valid access token is required")
		return caller{}, false
	}
	if !slices.Contains(authCtx.RolesLive, adminRoleName) {
		httperr.Write(r.Context(), w, http.StatusForbidden, "forbidden", "admin role required")
		return caller{}, false
	}
	return caller{tenant: tenantCtx, auth: authCtx}, true
}

// ServeGet is GET /admin/settings.
func (h *Handler) ServeGet(w http.ResponseWriter, r *http.Request) {
	c, ok := h.authorize(w, r)
	if !ok {
		return
	}
	settings, err := h.load(r.Context(), c.tenant)
	if err != nil {
		writeInternalError(w, r, err, "load settings")
		return
	}
	writeJSON(w, http.StatusOK, settings)
}

// ServePatch is PATCH /admin/settings: every field present in the body
// is validated, then written; an absent field keeps its value. The
// response is the settings as they now stand.
func (h *Handler) ServePatch(w http.ResponseWriter, r *http.Request) {
	c, ok := h.authorize(w, r)
	if !ok {
		return
	}
	ctx := r.Context()

	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	var body patchRequest
	if err := json.UnmarshalRead(r.Body, &body, json.RejectUnknownMembers(true)); err != nil {
		httperr.Write(r.Context(), w, http.StatusBadRequest, "invalid_request", "malformed request body")
		return
	}

	current, err := h.load(ctx, c.tenant)
	if err != nil {
		writeInternalError(w, r, err, "load settings")
		return
	}
	plan, ferr, err := h.plan(ctx, c, loginsession.ClientIP(r), current, body)
	if err != nil {
		writeInternalError(w, r, err, "validate settings")
		return
	}
	if ferr != nil {
		writeFieldError(w, r, ferr)
		return
	}

	committed, err := h.apply(ctx, c.tenant, plan)
	if plan.profile != nil {
		h.invalidateDomainCache(ctx, c.tenant.TenantID)
	}
	if len(committed) > 0 {
		h.recordAudit(r, c, "tenant.settings_updated", map[string]any{"fields": committed})
	}
	if err != nil {
		writeInternalError(w, r, err, "save settings")
		return
	}

	updated, err := h.load(ctx, c.tenant)
	if err != nil {
		writeInternalError(w, r, err, "reload settings")
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

// invalidateDomainCache drops tenantID's cached tenantresolve entries,
// which carry the tenant's name and country — best-effort, since the change itself
// has committed and a stale name only lasts the cache TTL.
func (h *Handler) invalidateDomainCache(ctx context.Context, tenantID string) {
	if h.deps.Cache == nil {
		return
	}
	if err := tenantresolve.InvalidateTenantDomains(ctx, h.deps.TenantStore, h.deps.Cache, tenantID); err != nil {
		log.Warn().Err(err).Str("tenant_id", tenantID).Msg("adminsettings: invalidate domain cache")
	}
}

// recordAudit writes an auth audit row for a settings change. The change
// has already committed, so a failed write is logged, not returned.
func (h *Handler) recordAudit(r *http.Request, c caller, eventType string, metadata map[string]any) {
	if h.deps.Audit == nil {
		return
	}
	metadata["performed_by"] = c.auth.UserID
	raw, err := json.Marshal(metadata)
	if err != nil {
		log.Error().Err(err).Msg("adminsettings: encode audit metadata")
		return
	}
	if err := h.deps.Audit.Insert(r.Context(), authaudit.Row{
		EventType: eventType,
		TenantID:  c.tenant.TenantID,
		UserID:    c.auth.UserID,
		SessionID: c.auth.SessionID,
		IPAddress: loginsession.ClientIP(r),
		UserAgent: r.UserAgent(),
		Success:   true,
		Metadata:  raw,
	}); err != nil {
		log.Error().Err(err).Str("tenant_id", c.tenant.TenantID).Str("event", eventType).Msg("adminsettings: write audit row")
	}
}
