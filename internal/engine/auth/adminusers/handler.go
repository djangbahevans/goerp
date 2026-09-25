// Package adminusers implements the tenant admin user endpoints
// (shell-ux.md §5.1): the user directory and detail, suspend, unsuspend
// and soft-delete (auth-internals.md §2 "User status lifecycle"), one
// user's session list and revoke (auth-internals.md §4 "Session
// management endpoints"), and inviting users (auth-internals.md §3
// "Invite flow").
//
// Like internal/engine/auth/mfareset, these are Class A tenant-facing
// routes, under "/admin/" or "/users/": Host-header tenant resolution,
// session authentication and the admin role in the resolved tenant.
package adminusers

import (
	"cmp"
	"context"
	"encoding/base64"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"
	"uuid"

	"github.com/rs/zerolog/log"

	"github.com/djangbahevans/goerp/internal/engine/auth/authcheck"
	"github.com/djangbahevans/goerp/internal/engine/auth/authme"
	"github.com/djangbahevans/goerp/internal/engine/auth/loginsession"
	"github.com/djangbahevans/goerp/internal/engine/auth/session"
	"github.com/djangbahevans/goerp/internal/engine/auth/sessionrevoke"
	"github.com/djangbahevans/goerp/internal/engine/authaudit"
	"github.com/djangbahevans/goerp/internal/engine/files"
	"github.com/djangbahevans/goerp/internal/engine/invite"
	"github.com/djangbahevans/goerp/internal/engine/role"
	"github.com/djangbahevans/goerp/internal/engine/route"
	"github.com/djangbahevans/goerp/internal/engine/storage"
	tenantresolve "github.com/djangbahevans/goerp/internal/engine/tenant/resolve"
	"github.com/djangbahevans/goerp/internal/engine/user"
)

const (
	maxBodyBytes     = 64 * 1024
	adminRoleName    = "admin"
	defaultListLimit = 50
	maxListLimit     = 100
)

var listStatuses = []string{"active", "invited", "suspended"}

type Handler struct {
	tenants  *tenantresolve.Resolver
	auth     *authcheck.Checker
	store    *Store
	roles    *role.Store
	sessions *session.Store
	revoker  *sessionrevoke.Revoker
	invites  *invite.Store
	users    *user.Store
	files    *files.Store
	backend  storage.Backend
}

func NewHandler(tenants *tenantresolve.Resolver, auth *authcheck.Checker, store *Store, roles *role.Store, sessions *session.Store, revoker *sessionrevoke.Revoker, invites *invite.Store, users *user.Store, filesStore *files.Store, backend storage.Backend) *Handler {
	return &Handler{
		tenants:  tenants,
		auth:     auth,
		store:    store,
		roles:    roles,
		sessions: sessions,
		revoker:  revoker,
		invites:  invites,
		users:    users,
		files:    filesStore,
		backend:  backend,
	}
}

type userJSON struct {
	ID           string     `json:"id"`
	Name         *string    `json:"name"`
	AvatarURL    *string    `json:"avatar_url"`
	Email        string     `json:"email"`
	Roles        []string   `json:"roles"`
	Status       string     `json:"status"`
	LastLoginAt  *time.Time `json:"last_login_at"`
	InvitationID *string    `json:"invitation_id"`
}

type invitationJSON struct {
	ID        string    `json:"id"`
	Role      string    `json:"role"`
	ExpiresAt time.Time `json:"expires_at"`
	CreatedAt time.Time `json:"created_at"`
}

type userDetailJSON struct {
	userJSON
	Phone      *string         `json:"phone"`
	Invitation *invitationJSON `json:"invitation"`
}

type sessionJSON struct {
	ID           string    `json:"id"`
	UserAgent    *string   `json:"user_agent"`
	IPAddress    *string   `json:"ip_address"`
	CountryCode  *string   `json:"country_code"`
	SignedInAt   time.Time `json:"signed_in_at"`
	LastActiveAt time.Time `json:"last_active_at"`
	Persistent   bool      `json:"persistent"`
	Current      bool      `json:"current"`
}

type suspendRequest struct {
	Reason string `json:"reason"`
}

// writeJSON matches encoding/json v1's Encoder defaults, the same as
// mfareset's own writeJSON.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.MarshalWrite(w, v, jsontext.EscapeForHTML(true), jsontext.EscapeForJS(true))
}

func writeJSONError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]any{
		"error": map[string]string{"code": code, "message": message},
	})
}

func writeInternalError(w http.ResponseWriter, err error, msg string) {
	log.Error().Err(err).Msg("adminusers: " + msg)
	writeJSONError(w, http.StatusInternalServerError, "internal_error", "request failed")
}

type caller struct {
	tenant *tenantresolve.TenantContext
	auth   *authcheck.AuthContext
}

// authorize resolves the tenant from Host, authenticates the access token
// and requires the admin role in that tenant.
func (h *Handler) authorize(w http.ResponseWriter, r *http.Request) (caller, bool) {
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
			writeInternalError(w, err, "tenant resolution failed")
		}
		return caller{}, false
	}

	rawToken := authcheck.ExtractToken(r)
	if rawToken == "" {
		writeJSONError(w, http.StatusUnauthorized, "unauthenticated", "a valid access token is required")
		return caller{}, false
	}
	authCtx, err := h.auth.Authenticate(ctx, rawToken, tenantCtx.TenantID, tenantCtx.Slug, loginsession.ClientIP(r), nil, nil)
	if err != nil || !authCtx.IsAuthenticated {
		writeJSONError(w, http.StatusUnauthorized, "unauthenticated", "a valid access token is required")
		return caller{}, false
	}
	if !slices.Contains(authCtx.RolesLive, adminRoleName) {
		writeJSONError(w, http.StatusForbidden, "forbidden", "admin role required")
		return caller{}, false
	}
	return caller{tenant: tenantCtx, auth: authCtx}, true
}

// targetID returns the {id} path parameter when it is a well-formed UUID;
// anything else can't name a user and is a 404.
func targetID(w http.ResponseWriter, r *http.Request) (string, bool) {
	id := route.ParamsFromContext(r.Context())["id"]
	if _, err := uuid.Parse(id); err != nil {
		writeJSONError(w, http.StatusNotFound, "not_found", "not found")
		return "", false
	}
	return id, true
}

// member returns the {id} path parameter when it names a non-deleted
// member of the caller's tenant. users.id is global, so membership is
// checked before anything reads or changes the target.
func (h *Handler) member(w http.ResponseWriter, r *http.Request, c caller) (entry, bool) {
	id, ok := targetID(w, r)
	if !ok {
		return entry{}, false
	}
	e, err := h.store.get(r.Context(), c.tenant.Slug, id)
	if errors.Is(err, errUserNotFound) || (err == nil && e.InvitationID != nil) {
		writeJSONError(w, http.StatusNotFound, "not_found", "not found")
		return entry{}, false
	}
	if err != nil {
		writeInternalError(w, err, "target lookup failed")
		return entry{}, false
	}
	return e, true
}

func (h *Handler) toJSON(r *http.Request, tenantSlug string, e entry) userJSON {
	var avatarURL *string
	if e.AvatarFileID != nil {
		avatarURL = authme.AvatarURL(r.Context(), h.files, h.backend, tenantSlug, e.ID, *e.AvatarFileID)
	}
	return userJSON{
		ID:           e.ID,
		Name:         e.Name,
		AvatarURL:    avatarURL,
		Email:        e.Email,
		Roles:        e.Roles,
		Status:       e.Status,
		LastLoginAt:  e.LastLoginAt,
		InvitationID: e.InvitationID,
	}
}

// ServeList is GET /admin/users.
func (h *Handler) ServeList(w http.ResponseWriter, r *http.Request) {
	c, ok := h.authorize(w, r)
	if !ok {
		return
	}

	q := r.URL.Query()
	filter := listFilter{Search: strings.TrimSpace(q.Get("q")), Status: q.Get("status"), Role: q.Get("role"), Limit: defaultListLimit}
	if filter.Status != "" && !slices.Contains(listStatuses, filter.Status) {
		writeJSONError(w, http.StatusBadRequest, "invalid_request", "status must be one of active, invited, suspended")
		return
	}
	if raw := q.Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > maxListLimit {
			writeJSONError(w, http.StatusBadRequest, "invalid_request", "limit must be an integer from 1 to 100")
			return
		}
		filter.Limit = n
	}
	if raw := q.Get("cursor"); raw != "" {
		after, err := base64.RawURLEncoding.DecodeString(raw)
		if err != nil || len(after) == 0 {
			writeJSONError(w, http.StatusBadRequest, "invalid_request", "malformed cursor")
			return
		}
		filter.After = string(after)
	}

	entries, total, err := h.store.list(r.Context(), c.tenant.Slug, filter)
	if err != nil {
		writeInternalError(w, err, "list users failed")
		return
	}

	data := make([]userJSON, len(entries))
	for i, e := range entries {
		data[i] = h.toJSON(r, c.tenant.Slug, e)
	}
	var cursor *string
	if len(entries) == filter.Limit {
		cursor = new(base64.RawURLEncoding.EncodeToString([]byte(entries[len(entries)-1].Email)))
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"data": data,
		"meta": map[string]any{"total": total, "cursor": cursor},
	})
}

// ServeGet is GET /admin/users/{id}.
func (h *Handler) ServeGet(w http.ResponseWriter, r *http.Request) {
	c, ok := h.authorize(w, r)
	if !ok {
		return
	}
	id, ok := targetID(w, r)
	if !ok {
		return
	}
	ctx := r.Context()

	e, err := h.store.get(ctx, c.tenant.Slug, id)
	if errors.Is(err, errUserNotFound) {
		writeJSONError(w, http.StatusNotFound, "not_found", "not found")
		return
	}
	if err != nil {
		writeInternalError(w, err, "get user failed")
		return
	}
	inv, err := h.store.liveInvitation(ctx, c.tenant.Slug, e.Email)
	if err != nil {
		writeInternalError(w, err, "get invitation failed")
		return
	}

	detail := userDetailJSON{userJSON: h.toJSON(r, c.tenant.Slug, e), Phone: e.Phone}
	if inv != nil {
		detail.Invitation = &invitationJSON{ID: inv.ID, Role: inv.Role, ExpiresAt: inv.ExpiresAt, CreatedAt: inv.CreatedAt}
	}
	writeJSON(w, http.StatusOK, detail)
}

func (h *Handler) auditRow(r *http.Request, c caller, eventType, targetID string, metadata map[string]any) authaudit.Row {
	raw, _ := json.Marshal(metadata)
	return authaudit.Row{
		EventType: eventType,
		TenantID:  c.tenant.TenantID,
		UserID:    targetID,
		IPAddress: loginsession.ClientIP(r),
		UserAgent: r.UserAgent(),
		Success:   true,
		Metadata:  raw,
	}
}

// changeStatus is the shared body of suspend, unsuspend and delete: the
// guarded status change and its audit row commit together. When
// revokeReason is set, the target's sessions in this tenant are revoked
// first, so a revocation failure leaves the status unchanged and the
// request retryable.
func (h *Handler) changeStatus(w http.ResponseWriter, r *http.Request, c caller, targetID string, run func() error, conflictCode, revokeReason string) {
	if revokeReason != "" {
		if err := h.revoker.RevokeAllForUserInTenant(r.Context(), targetID, c.tenant.TenantID, revokeReason); err != nil {
			writeInternalError(w, err, "session revocation failed")
			return
		}
	}
	if err := run(); err != nil {
		if errors.Is(err, errStateChanged) {
			writeJSONError(w, http.StatusConflict, conflictCode, "the user's status doesn't allow this change")
			return
		}
		writeInternalError(w, err, "status change failed")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func rejectSelf(w http.ResponseWriter, c caller, target entry) bool {
	if target.ID != c.auth.UserID {
		return false
	}
	writeJSONError(w, http.StatusBadRequest, "cannot_modify_self", "an admin can't suspend or delete their own account")
	return true
}

// ServeSuspend is POST /admin/users/{id}/suspend.
func (h *Handler) ServeSuspend(w http.ResponseWriter, r *http.Request) {
	c, ok := h.authorize(w, r)
	if !ok {
		return
	}
	target, ok := h.member(w, r, c)
	if !ok || rejectSelf(w, c, target) {
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	var body suspendRequest
	if err := json.UnmarshalRead(r.Body, &body); err != nil || strings.TrimSpace(body.Reason) == "" {
		writeJSONError(w, http.StatusBadRequest, "invalid_request", "a reason is required")
		return
	}

	row := h.auditRow(r, c, "user.suspended", target.ID, map[string]any{"performed_by": c.auth.UserID, "reason": strings.TrimSpace(body.Reason)})
	run := func() error { return h.store.suspend(r.Context(), target.ID, row) }
	h.changeStatus(w, r, c, target.ID, run, "user_not_active", "admin_suspend")
}

// ServeUnsuspend is POST /admin/users/{id}/unsuspend.
func (h *Handler) ServeUnsuspend(w http.ResponseWriter, r *http.Request) {
	c, ok := h.authorize(w, r)
	if !ok {
		return
	}
	target, ok := h.member(w, r, c)
	if !ok {
		return
	}

	row := h.auditRow(r, c, "user.unsuspended", target.ID, map[string]any{"performed_by": c.auth.UserID})
	run := func() error { return h.store.unsuspend(r.Context(), target.ID, row) }
	h.changeStatus(w, r, c, target.ID, run, "user_not_suspended", "")
}

// ServeDelete is DELETE /admin/users/{id}.
func (h *Handler) ServeDelete(w http.ResponseWriter, r *http.Request) {
	c, ok := h.authorize(w, r)
	if !ok {
		return
	}
	target, ok := h.member(w, r, c)
	if !ok || rejectSelf(w, c, target) {
		return
	}

	row := h.auditRow(r, c, "user.deleted", target.ID, map[string]any{"performed_by": c.auth.UserID})
	run := func() error { return h.store.softDelete(r.Context(), target.ID, row) }
	h.changeStatus(w, r, c, target.ID, run, "user_already_deleted", "admin_delete")
}

// ServeSessions is GET /admin/users/{id}/sessions.
func (h *Handler) ServeSessions(w http.ResponseWriter, r *http.Request) {
	c, ok := h.authorize(w, r)
	if !ok {
		return
	}
	target, ok := h.member(w, r, c)
	if !ok {
		return
	}

	ctx := r.Context()
	families, err := h.sessions.LiveFamiliesForUserInTenant(ctx, target.ID, c.tenant.TenantID)
	if err != nil {
		writeInternalError(w, err, "list sessions failed")
		return
	}
	currentFamily, err := h.callerFamily(ctx, c, target.ID)
	if err != nil {
		writeInternalError(w, err, "current session lookup failed")
		return
	}
	out := make([]sessionJSON, len(families))
	for i, f := range families {
		out[i] = sessionJSON{
			ID:           f.ID,
			UserAgent:    f.UserAgent,
			IPAddress:    f.IPAddress,
			CountryCode:  f.CountryCode,
			SignedInAt:   f.SignedInAt,
			LastActiveAt: f.LastActiveAt,
			Persistent:   f.Persistent,
			Current:      f.ID == currentFamily,
		}
	}
	// Stable, so the current session moves first and the rest keep the
	// store's most-recently-active order.
	slices.SortStableFunc(out, func(a, b sessionJSON) int {
		return cmp.Compare(boolRank(b.Current), boolRank(a.Current))
	})
	writeJSON(w, http.StatusOK, map[string]any{"sessions": out})
}

// callerFamily returns the caller's own session family when targetID is
// the caller, and "" otherwise.
func (h *Handler) callerFamily(ctx context.Context, c caller, targetID string) (string, error) {
	if targetID != c.auth.UserID || c.auth.SessionID == "" {
		return "", nil
	}
	familyID, err := h.sessions.FamilyIDForSession(ctx, c.auth.SessionID)
	if errors.Is(err, session.ErrSessionNotFound) {
		return "", nil
	}
	return familyID, err
}

func boolRank(b bool) int {
	if b {
		return 1
	}
	return 0
}

// ServeRevokeSession is DELETE /admin/users/{id}/sessions/{family_id}.
func (h *Handler) ServeRevokeSession(w http.ResponseWriter, r *http.Request) {
	c, ok := h.authorize(w, r)
	if !ok {
		return
	}
	target, ok := h.member(w, r, c)
	if !ok {
		return
	}
	ctx := r.Context()

	familyID := route.ParamsFromContext(ctx)["family_id"]
	if _, err := uuid.Parse(familyID); err != nil {
		writeJSONError(w, http.StatusNotFound, "session_not_found", "session not found")
		return
	}
	family, err := h.sessions.LiveFamilyForUserInTenant(ctx, target.ID, c.tenant.TenantID, familyID)
	if errors.Is(err, session.ErrSessionNotFound) {
		writeJSONError(w, http.StatusNotFound, "session_not_found", "session not found")
		return
	}
	if err != nil {
		writeInternalError(w, err, "session lookup failed")
		return
	}
	currentFamily, err := h.callerFamily(ctx, c, target.ID)
	if err != nil {
		writeInternalError(w, err, "current session lookup failed")
		return
	}
	if family.ID == currentFamily {
		writeJSONError(w, http.StatusBadRequest, "cannot_revoke_current_session", "sign out to end the current session")
		return
	}

	if err := h.revoker.RevokeFamily(ctx, family.ID, "admin"); err != nil {
		writeInternalError(w, err, "session revocation failed")
		return
	}
	h.recordSessionRevoked(r, c, target.ID, family)
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) recordSessionRevoked(r *http.Request, c caller, targetID string, family session.Family) {
	row := h.auditRow(r, c, "session.revoked", targetID, map[string]any{"performed_by": c.auth.UserID, "family_id": family.ID})
	row.SessionID = family.LiveRowID
	if err := h.store.audit.Insert(r.Context(), row); err != nil {
		log.Warn().Err(err).Str("tenant", c.tenant.Slug).Msg("adminusers: session.revoked audit insert failed")
	}
}
