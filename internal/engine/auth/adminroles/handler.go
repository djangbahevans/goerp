// Package adminroles implements the tenant admin role endpoints
// (shell-ux.md §5.2): list, detail, create, update and delete a tenant's
// roles, and the catalog of permissions a role can be granted
// (auth-internals.md §10).
//
// Like internal/engine/auth/mfareset, these are Class A tenant-facing
// routes despite the "/admin/" prefix: Host-header tenant resolution,
// session authentication and the admin role in the resolved tenant.
//
// A role write commits with its auth audit row and a
// permcache.RolesChangedChannel notification, then rebuilds this
// replica's cache layer 3 (auth-internals.md §14) for the tenant; every
// other replica rebuilds from the notification. A permission or name
// change also marks each holder's sessions stale and tells their open
// shells to refresh, so the change applies without signing out.
package adminroles

import (
	"cmp"
	"context"
	"database/sql"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"
	"uuid"

	"github.com/rs/zerolog/log"

	"github.com/djangbahevans/goerp/internal/engine/auth/authcheck"
	"github.com/djangbahevans/goerp/internal/engine/auth/loginsession"
	"github.com/djangbahevans/goerp/internal/engine/auth/sessionrevoke"
	"github.com/djangbahevans/goerp/internal/engine/authaudit"
	"github.com/djangbahevans/goerp/internal/engine/module"
	"github.com/djangbahevans/goerp/internal/engine/permcache"
	"github.com/djangbahevans/goerp/internal/engine/permission"
	"github.com/djangbahevans/goerp/internal/engine/registry"
	"github.com/djangbahevans/goerp/internal/engine/role"
	"github.com/djangbahevans/goerp/internal/engine/route"
	tenantresolve "github.com/djangbahevans/goerp/internal/engine/tenant/resolve"
	"github.com/djangbahevans/goerp/internal/engine/ws"
)

const (
	maxBodyBytes          = 64 * 1024
	adminRoleName         = "admin"
	maxDescriptionLength  = 500
	roleChangedEvent      = "role.changed"
	roleChangedPermission = "permissions_changed"
)

// roleNamePattern matches the built-in names' shape. A role name travels
// in URL paths (DELETE /admin/users/{id}/roles/{role}) and the JWT roles
// claim, so it stays a plain identifier.
var roleNamePattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,62}$`)

// reservedRoleNames are never roles rows (auth-internals.md §10 "Built-in
// roles"), so no custom role may take them.
var reservedRoleNames = []string{"superadmin", "public"}

// AuditRecorder is satisfied by authaudit.Store.
type AuditRecorder interface {
	InsertTx(ctx context.Context, tx *sql.Tx, row authaudit.Row) error
}

// Modules is satisfied by registry.ModuleRegistry.
type Modules interface {
	Snapshot() *registry.RegistrySnapshot
}

type Handler struct {
	tenants  *tenantresolve.Resolver
	auth     *authcheck.Checker
	roles    *role.Store
	modules  Modules
	perms    *permcache.RolePermissionMap
	sessions *sessionrevoke.Revoker
	hub      *ws.Hub
	audit    AuditRecorder
}

func NewHandler(tenants *tenantresolve.Resolver, auth *authcheck.Checker, roles *role.Store, modules Modules, perms *permcache.RolePermissionMap, sessions *sessionrevoke.Revoker, hub *ws.Hub, audit AuditRecorder) *Handler {
	return &Handler{tenants: tenants, auth: auth, roles: roles, modules: modules, perms: perms, sessions: sessions, hub: hub, audit: audit}
}

type roleJSON struct {
	ID              string  `json:"id"`
	Name            string  `json:"name"`
	Description     *string `json:"description"`
	IsImmutable     bool    `json:"is_immutable"`
	UserCount       int     `json:"user_count"`
	InvitationCount int     `json:"invitation_count"`
}

type roleDetailJSON struct {
	roleJSON
	Permissions []string `json:"permissions"`
}

type catalogEntryJSON struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Category    string `json:"category"`
	Module      string `json:"module"`
}

type createRequest struct {
	Name        string   `json:"name"`
	Description *string  `json:"description"`
	Permissions []string `json:"permissions"`
}

type updateRequest struct {
	Name        *string   `json:"name"`
	Description *string   `json:"description"`
	Permissions *[]string `json:"permissions"`
}

func toJSON(s role.Summary) roleJSON {
	return roleJSON{ID: s.ID, Name: s.Name, Description: s.Description, IsImmutable: s.IsImmutable, UserCount: s.UserCount, InvitationCount: s.InvitationCount}
}

func toDetailJSON(d role.Detail) roleDetailJSON {
	return roleDetailJSON{roleJSON: toJSON(d.Summary), Permissions: d.Permissions}
}

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
	log.Error().Err(err).Msg("adminroles: " + msg)
	writeJSONError(w, http.StatusInternalServerError, "internal_error", "request failed")
}

type caller struct {
	tenant *tenantresolve.TenantContext
	auth   *authcheck.AuthContext
}

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

func roleID(w http.ResponseWriter, r *http.Request) (string, bool) {
	id := route.ParamsFromContext(r.Context())["id"]
	if _, err := uuid.Parse(id); err != nil {
		writeJSONError(w, http.StatusNotFound, "not_found", "not found")
		return "", false
	}
	return id, true
}

// registry returns the current snapshot's permission registry, empty
// before the engine has published one.
func (h *Handler) registry() *permission.PermissionRegistry {
	if snap := h.modules.Snapshot(); snap != nil {
		if reg := snap.PermissionRegistry(); reg != nil {
			return reg
		}
	}
	return permission.NewPermissionRegistry()
}

// validName trims name and reports whether it's usable; the message
// explains a rejection.
func validName(name string) (string, string) {
	name = strings.TrimSpace(name)
	switch {
	case !roleNamePattern.MatchString(name):
		return name, "a role name is 1 to 63 lowercase letters, digits or underscores, starting with a letter"
	case slices.Contains(reservedRoleNames, name):
		return name, "that role name is reserved"
	}
	return name, ""
}

// validDescription trims description, storing an empty one as NULL.
func validDescription(description *string) (*string, bool) {
	if description == nil {
		return nil, true
	}
	trimmed := strings.TrimSpace(*description)
	if utf8.RuneCountInString(trimmed) > maxDescriptionLength {
		return nil, false
	}
	if trimmed == "" {
		return nil, true
	}
	return &trimmed, true
}

// catalog is every permission the tenant's ready, enabled modules declare,
// by module then name.
func (h *Handler) catalog(c caller) []catalogEntryJSON {
	entries := []catalogEntryJSON{}
	snap := h.modules.Snapshot()
	if snap == nil || snap.PermissionRegistry() == nil {
		return entries
	}
	reg := snap.PermissionRegistry()
	for name, m := range snap.Modules() {
		if m.Status != module.StatusReady || !c.tenant.Entitlements.ModuleEnabled(name) {
			continue
		}
		for _, p := range reg.ModulePermissions(name) {
			entries = append(entries, catalogEntryJSON{Name: p.Name, Description: p.Description, Category: p.Category, Module: name})
		}
	}
	slices.SortFunc(entries, func(a, b catalogEntryJSON) int {
		return cmp.Or(cmp.Compare(a.Module, b.Module), cmp.Compare(a.Name, b.Name))
	})
	return entries
}

// unknownPermissions returns the names outside the tenant's catalog. held
// (the role's current grants) stays allowed, so disabling a module doesn't
// make every later edit of a role that holds its permissions fail.
func (h *Handler) unknownPermissions(c caller, names, held []string) []string {
	allowed := map[string]bool{}
	for _, entry := range h.catalog(c) {
		allowed[entry.Name] = true
	}
	for _, name := range held {
		allowed[name] = true
	}
	var unknown []string
	for _, name := range names {
		if !allowed[name] {
			unknown = append(unknown, name)
		}
	}
	return unknown
}

func (h *Handler) auditRow(r *http.Request, c caller, eventType string, metadata map[string]any) authaudit.Row {
	metadata["performed_by"] = c.auth.UserID
	raw, _ := json.Marshal(metadata)
	return authaudit.Row{
		EventType: eventType,
		TenantID:  c.tenant.TenantID,
		UserID:    c.auth.UserID,
		SessionID: c.auth.SessionID,
		IPAddress: loginsession.ClientIP(r),
		UserAgent: r.UserAgent(),
		Success:   true,
		Metadata:  raw,
	}
}

// commit is every write's in-transaction tail: the audit row and the
// cross-replica announcement.
func (h *Handler) commit(ctx context.Context, tx *sql.Tx, c caller, row authaudit.Row) error {
	if err := h.audit.InsertTx(ctx, tx, row); err != nil {
		return err
	}
	return permcache.NotifyRolesChanged(ctx, tx, c.tenant.Slug)
}

// rebuild refreshes this replica's role permission map once a write has
// committed. A failure is logged: the notification the write sent makes
// every replica's listener, this one's included, rebuild again.
func (h *Handler) rebuild(ctx context.Context, c caller) {
	if err := h.perms.RebuildTenant(ctx, h.roles, h.registry, c.tenant.Slug); err != nil {
		log.Warn().Err(err).Str("tenant", c.tenant.Slug).Msg("adminroles: role permission map rebuild failed")
	}
}

// refreshHolders marks every holder's sessions stale and tells their open
// shells to refetch permissions (the same role.changed event
// internal/engine/auth/roleassign sends). Best-effort: the change is
// already committed and the map rebuilt.
func (h *Handler) refreshHolders(ctx context.Context, c caller, roleID, roleName string) {
	holders, err := h.roles.UserIDsWithRole(ctx, c.tenant.Slug, roleID)
	if err != nil {
		log.Warn().Err(err).Str("tenant", c.tenant.Slug).Msg("adminroles: listing role holders failed")
		return
	}
	for _, userID := range holders {
		if err := h.sessions.MarkRolesStaleForUserInTenant(ctx, userID, c.tenant.TenantID); err != nil {
			log.Warn().Err(err).Str("tenant", c.tenant.Slug).Str("user", userID).Msg("adminroles: mark roles stale failed")
		}
		if h.hub == nil {
			continue
		}
		payload := map[string]string{"role": roleName, "action": roleChangedPermission}
		if _, err := h.hub.Broadcast(ctx, ws.UserChannel(userID), roleChangedEvent, payload); err != nil {
			log.Warn().Err(err).Str("tenant", c.tenant.Slug).Str("user", userID).Msg("adminroles: broadcast failed")
		}
	}
}

// ServeList is GET /admin/roles.
func (h *Handler) ServeList(w http.ResponseWriter, r *http.Request) {
	c, ok := h.authorize(w, r)
	if !ok {
		return
	}
	roles, err := h.roles.ListRoles(r.Context(), c.tenant.Slug)
	if err != nil {
		writeInternalError(w, err, "list roles failed")
		return
	}
	data := make([]roleJSON, len(roles))
	for i, s := range roles {
		data[i] = toJSON(s)
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": data})
}

// ServeGet is GET /admin/roles/{id}.
func (h *Handler) ServeGet(w http.ResponseWriter, r *http.Request) {
	c, ok := h.authorize(w, r)
	if !ok {
		return
	}
	id, ok := roleID(w, r)
	if !ok {
		return
	}
	detail, err := h.roles.GetRole(r.Context(), c.tenant.Slug, id)
	if errors.Is(err, role.ErrRoleNotFound) {
		writeJSONError(w, http.StatusNotFound, "not_found", "not found")
		return
	}
	if err != nil {
		writeInternalError(w, err, "get role failed")
		return
	}
	writeJSON(w, http.StatusOK, toDetailJSON(detail))
}

// ServePermissions is GET /admin/roles/permissions: every permission the
// tenant's enabled modules declare, by module then name.
func (h *Handler) ServePermissions(w http.ResponseWriter, r *http.Request) {
	c, ok := h.authorize(w, r)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"permissions": h.catalog(c)})
}

// ServeCreate is POST /admin/roles.
func (h *Handler) ServeCreate(w http.ResponseWriter, r *http.Request) {
	c, ok := h.authorize(w, r)
	if !ok {
		return
	}
	ctx := r.Context()

	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	var body createRequest
	if err := json.UnmarshalRead(r.Body, &body); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid_request", "malformed request body")
		return
	}
	name, problem := validName(body.Name)
	if problem != "" {
		writeJSONError(w, http.StatusBadRequest, "invalid_name", problem)
		return
	}
	description, ok := validDescription(body.Description)
	if !ok {
		writeJSONError(w, http.StatusBadRequest, "invalid_description", "a description is at most 500 characters")
		return
	}
	if unknown := h.unknownPermissions(c, body.Permissions, nil); len(unknown) > 0 {
		writeJSONError(w, http.StatusBadRequest, "unknown_permission", "unknown permission: "+strings.Join(unknown, ", "))
		return
	}

	id, err := h.roles.CreateRole(ctx, c.tenant.Slug, name, description, body.Permissions, c.auth.UserID, func(tx *sql.Tx, id string) error {
		return h.commit(ctx, tx, c, h.auditRow(r, c, "role.created", map[string]any{"role_id": id, "name": name, "permissions": body.Permissions}))
	})
	if errors.Is(err, role.ErrRoleNameTaken) {
		writeJSONError(w, http.StatusConflict, "role_name_taken", "a role with that name already exists")
		return
	}
	if err != nil {
		writeInternalError(w, err, "create role failed")
		return
	}
	h.rebuild(ctx, c)
	h.respondWithRole(w, r, c, id, http.StatusCreated)
}

// ServeUpdate is PATCH /admin/roles/{id}.
func (h *Handler) ServeUpdate(w http.ResponseWriter, r *http.Request) {
	c, ok := h.authorize(w, r)
	if !ok {
		return
	}
	id, ok := roleID(w, r)
	if !ok {
		return
	}
	ctx := r.Context()

	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	var body updateRequest
	if err := json.UnmarshalRead(r.Body, &body); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid_request", "malformed request body")
		return
	}
	change := role.Change{Permissions: body.Permissions}
	metadata := map[string]any{"role_id": id}
	if body.Name != nil {
		name, problem := validName(*body.Name)
		if problem != "" {
			writeJSONError(w, http.StatusBadRequest, "invalid_name", problem)
			return
		}
		change.Name = &name
		metadata["name"] = name
	}
	if body.Description != nil {
		description, ok := validDescription(body.Description)
		if !ok {
			writeJSONError(w, http.StatusBadRequest, "invalid_description", "a description is at most 500 characters")
			return
		}
		change.Description = new(derefOr(description))
	}
	if body.Permissions != nil {
		current, err := h.roles.GetRole(ctx, c.tenant.Slug, id)
		if errors.Is(err, role.ErrRoleNotFound) {
			writeJSONError(w, http.StatusNotFound, "not_found", "not found")
			return
		}
		if err != nil {
			writeInternalError(w, err, "get role failed")
			return
		}
		if unknown := h.unknownPermissions(c, *body.Permissions, current.Permissions); len(unknown) > 0 {
			writeJSONError(w, http.StatusBadRequest, "unknown_permission", "unknown permission: "+strings.Join(unknown, ", "))
			return
		}
		metadata["permissions"] = *body.Permissions
	}

	err := h.roles.UpdateRole(ctx, c.tenant.Slug, id, change, c.auth.UserID, func(tx *sql.Tx) error {
		return h.commit(ctx, tx, c, h.auditRow(r, c, "role.updated", metadata))
	})
	switch {
	case errors.Is(err, role.ErrRoleNotFound):
		writeJSONError(w, http.StatusNotFound, "not_found", "not found")
		return
	case errors.Is(err, role.ErrRoleImmutable):
		writeJSONError(w, http.StatusForbidden, "role_immutable", "built-in roles can't be changed")
		return
	case errors.Is(err, role.ErrRoleNameTaken):
		writeJSONError(w, http.StatusConflict, "role_name_taken", "a role with that name already exists")
		return
	case err != nil:
		writeInternalError(w, err, "update role failed")
		return
	}
	h.rebuild(ctx, c)

	detail, err := h.roles.GetRole(ctx, c.tenant.Slug, id)
	if err != nil {
		writeInternalError(w, err, "reload role failed")
		return
	}
	if change.Name != nil || change.Permissions != nil {
		h.refreshHolders(ctx, c, id, detail.Name)
	}
	writeJSON(w, http.StatusOK, toDetailJSON(detail))
}

func derefOr(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// ServeDelete is DELETE /admin/roles/{id}.
func (h *Handler) ServeDelete(w http.ResponseWriter, r *http.Request) {
	c, ok := h.authorize(w, r)
	if !ok {
		return
	}
	id, ok := roleID(w, r)
	if !ok {
		return
	}
	ctx := r.Context()

	err := h.roles.DeleteRole(ctx, c.tenant.Slug, id, func(tx *sql.Tx) error {
		return h.commit(ctx, tx, c, h.auditRow(r, c, "role.deleted", map[string]any{"role_id": id}))
	})
	switch {
	case errors.Is(err, role.ErrRoleNotFound):
		writeJSONError(w, http.StatusNotFound, "not_found", "not found")
		return
	case errors.Is(err, role.ErrRoleImmutable):
		writeJSONError(w, http.StatusForbidden, "role_immutable", "built-in roles can't be deleted")
		return
	case errors.Is(err, role.ErrRoleInUse):
		writeJSONError(w, http.StatusConflict, "role_in_use", "remove this role from every user before deleting it")
		return
	case err != nil:
		writeInternalError(w, err, "delete role failed")
		return
	}
	h.rebuild(ctx, c)
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) respondWithRole(w http.ResponseWriter, r *http.Request, c caller, id string, status int) {
	detail, err := h.roles.GetRole(r.Context(), c.tenant.Slug, id)
	if err != nil {
		writeInternalError(w, err, "reload role failed")
		return
	}
	writeJSON(w, status, toDetailJSON(detail))
}
