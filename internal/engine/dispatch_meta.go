package engine

import (
	"context"
	"encoding/json/v2"
	"errors"
	"net/http"
	"sort"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/auth/authcheck"
	"github.com/djangbahevans/goerp/internal/engine/module"
	"github.com/djangbahevans/goerp/internal/engine/permission"
	"github.com/djangbahevans/goerp/internal/engine/recordshares"
	"github.com/djangbahevans/goerp/internal/engine/route"
	tenantresolve "github.com/djangbahevans/goerp/internal/engine/tenant/resolve"
	"github.com/djangbahevans/goerp/internal/engine/user"
	"github.com/djangbahevans/goerp/internal/engine/wasm"
	sdkmodel "github.com/djangbahevans/goerp/sdk/go/model"
	"go.opentelemetry.io/otel/trace"
)

// metaPermissionsResponse is GET /_meta/permissions' response shape —
// auth-internals.md's own worked example under "The /_meta/permissions
// endpoint" is the canonical response ({permissions, field_access} only);
// modules_enabled is multitenancy-internals.md §8 "Navigation
// filtering"'s addition, not part of that canonical example.
type metaPermissionsResponse struct {
	Permissions    []string                          `json:"permissions"`
	FieldAccess    map[string]map[string]fieldAccess `json:"field_access"`
	ModulesEnabled []string                          `json:"modules_enabled"`
}

type fieldAccess struct {
	Read  bool `json:"read"`
	Write bool `json:"write"`
}

// dispatchPermissionsRoute is GET /_meta/permissions' handler (goerp#417)
// — registered EngineNative but not EngineBuiltin (registry.go), so it
// rides the standard tenant/auth/permission middleware chain like any
// other Class A route (auth-internals.md §9) rather than resolving its
// own identity.
func (e *Engine) dispatchPermissionsRoute(w http.ResponseWriter, r *http.Request) {
	authCtx := authFromContext(r.Context())
	tenantCtx := tenantFromContext(r.Context())
	if authCtx == nil || tenantCtx == nil {
		// Unreachable via the real middleware chain — routeAuthMiddleware
		// requires Auth: "required" (registry.go's registration) before
		// this handler is ever reached. Guarded for direct-call
		// testability, matching dispatchORMRoute's own identical guard.
		writeRouteError(w, http.StatusServiceUnavailable, "not_ready", "tenant/auth context not resolved")
		return
	}

	snap := e.moduleRegistry.Snapshot()
	if snap == nil {
		writeRouteError(w, http.StatusServiceUnavailable, "not_ready", "engine has not finished starting")
		return
	}

	permReg := snap.PermissionRegistry()
	permissions := []string{}
	if permReg != nil {
		for _, name := range permReg.Names() {
			if hasPermission(permReg, authCtx, name) {
				permissions = append(permissions, name)
			}
		}
	}

	fieldAccessMap := map[string]map[string]fieldAccess{}
	if fieldSecReg := snap.FieldSecRegistry(); fieldSecReg != nil {
		for modelName, fields := range fieldSecReg.AllRules() {
			for fieldName, rule := range fields {
				read := rule.ReadPermission == "" || hasPermission(permReg, authCtx, rule.ReadPermission)
				write := rule.WritePermission == "" || hasPermission(permReg, authCtx, rule.WritePermission)
				if fieldAccessMap[modelName] == nil {
					fieldAccessMap[modelName] = map[string]fieldAccess{}
				}
				fieldAccessMap[modelName][fieldName] = fieldAccess{Read: read, Write: write}
			}
		}
	}

	modulesEnabled := []string{}
	for name, m := range snap.Modules() {
		if m.Status == module.StatusReady && tenantCtx.Entitlements.ModuleEnabled(name) {
			modulesEnabled = append(modulesEnabled, name)
		}
	}
	sort.Strings(modulesEnabled)

	writeJSON(w, http.StatusOK, metaPermissionsResponse{
		Permissions:    permissions,
		FieldAccess:    fieldAccessMap,
		ModulesEnabled: modulesEnabled,
	})
}

// hasPermission reports whether authCtx's caller holds permissionName,
// resolved against permReg's stable bitfield index — mirrors
// internal/engine/wasm/host_orm.go's unexported callerHasPermission,
// rewritten against the bare authcheck.AuthContext this package's
// dispatch handlers use rather than *wasm.ModuleContext (a different
// package, and callerHasPermission is unexported). Fails closed on a nil
// registry or unregistered name, same as that original.
func hasPermission(permReg *permission.PermissionRegistry, authCtx *authcheck.AuthContext, permissionName string) bool {
	if permReg == nil {
		return false
	}
	idx, ok := permReg.Index(permissionName)
	if !ok {
		return false
	}
	return authCtx.PermissionSet.Has(idx)
}

// shareCreateRequest is POST /_meta/shares' request body — view-system.md
// §12 "Document sharing": {model, record_id, user_email, permission,
// expires_at?}.
type shareCreateRequest struct {
	Model      string     `json:"model"`
	RecordID   string     `json:"record_id"`
	UserEmail  string     `json:"user_email"`
	Permission string     `json:"permission"`
	ExpiresAt  *time.Time `json:"expires_at,omitempty"`
}

type shareResponse struct {
	ID               string     `json:"id"`
	Model            string     `json:"model"`
	RecordID         string     `json:"record_id"`
	SharedWithUserID string     `json:"shared_with_user_id"`
	Permission       string     `json:"permission"`
	SharedBy         string     `json:"shared_by"`
	CreatedAt        time.Time  `json:"created_at"`
	ExpiresAt        *time.Time `json:"expires_at,omitempty"`
}

func shareToResponse(sh *recordshares.Share) shareResponse {
	return shareResponse{
		ID:               sh.ID,
		Model:            sh.Model,
		RecordID:         sh.RecordID,
		SharedWithUserID: sh.SharedWithUserID,
		Permission:       sh.Permission,
		SharedBy:         sh.SharedBy,
		CreatedAt:        sh.CreatedAt,
		ExpiresAt:        sh.ExpiresAt,
	}
}

// sharerCanReadRecord reports whether authCtx's caller can currently read
// recordID via host.orm.read — the same "current access" signal
// go-sdk-reference.md §22 "Document sharing" uses to cap POST
// /_meta/shares, reused by GET/DELETE so viewing or revoking a record's
// shares requires the same access a fresh share request against that
// record would. Fails closed (false) on an unresolvable model or any
// host error.
func (e *Engine) sharerCanReadRecord(ctx context.Context, authCtx *authcheck.AuthContext, tenantCtx *tenantresolve.TenantContext, modelName, recordID string) bool {
	snap := e.moduleRegistry.Snapshot()
	if snap == nil {
		return false
	}
	_, mod, _, ok := snap.ModelByName(modelName)
	if !ok {
		return false
	}

	traceID := trace.SpanFromContext(ctx).SpanContext().TraceID().String()
	modCtx := e.newModuleContext(ctx, EngineRequest{
		ID:            requestIDFromContext(ctx),
		UserID:        authCtx.UserID,
		PermissionSet: authCtx.PermissionSet,
		TenantID:      tenantCtx.TenantID,
		TenantSlug:    tenantCtx.Slug,
		TraceID:       traceID,
	}, mod)
	defer modCtx.RollbackAll()

	readOut, hostErr := wasm.ORMRead(ctx, e.primaryDB, e.cacheClient, modCtx, wasm.ORMReadInput{
		Model: modelName,
		IDs:   []string{recordID},
	})
	return hostErr == nil && len(readOut.Records) > 0
}

// dispatchSharesCreateRoute is POST /_meta/shares' handler (goerp#475) —
// same EngineNative-not-EngineBuiltin posture as dispatchPermissionsRoute
// above. Creates a record_shares grant after the permission-capping check
// go-sdk-reference.md §22 "Document sharing" specifies: reject a request
// for more access than the sharer currently has, checked via the
// sharer's own host.orm.read — the only "current access" signal
// available to native engine code, since there's no dry-run write-check
// host function to call instead. A read that succeeds is treated as
// sufficient grounds for either a read or a write share; the model's own
// .Shareable(perms...) declaration is what actually limits which
// permission levels are offered at all.
func (e *Engine) dispatchSharesCreateRoute(w http.ResponseWriter, r *http.Request) {
	authCtx := authFromContext(r.Context())
	tenantCtx := tenantFromContext(r.Context())
	if authCtx == nil || tenantCtx == nil {
		writeRouteError(w, http.StatusServiceUnavailable, "not_ready", "tenant/auth context not resolved")
		return
	}

	var body shareCreateRequest
	if err := json.UnmarshalRead(r.Body, &body); err != nil {
		writeRouteError(w, http.StatusBadRequest, "invalid_body", "request body must be a JSON object")
		return
	}
	if body.Model == "" || body.RecordID == "" || body.UserEmail == "" {
		writeRouteError(w, http.StatusBadRequest, "invalid_request", "model, record_id, and user_email are required")
		return
	}
	if body.Permission != string(sdkmodel.ReadShare) && body.Permission != string(sdkmodel.WriteShare) {
		writeRouteError(w, http.StatusBadRequest, "invalid_request", `permission must be "read" or "write"`)
		return
	}

	snap := e.moduleRegistry.Snapshot()
	if snap == nil {
		writeRouteError(w, http.StatusServiceUnavailable, "not_ready", "engine has not finished starting")
		return
	}
	_, _, md, ok := snap.ModelByName(body.Model)
	if !ok {
		writeRouteError(w, http.StatusBadRequest, "model_not_found", "unknown model: "+body.Model)
		return
	}
	if !md.Shareable {
		writeRouteError(w, http.StatusBadRequest, "not_shareable", body.Model+" is not declared .Shareable()")
		return
	}
	if md.Backend != "" {
		// .Shareable() widens a compiled RLS policy (view-system.md §12,
		// multitenancy-internals.md §5a) — meaningless for a Virtual or
		// Transient model, neither of which has a real Postgres table or
		// row-level security to widen.
		writeRouteError(w, http.StatusBadRequest, "not_shareable", body.Model+" is "+string(md.Backend)+"-backed; .Shareable() requires a Postgres-backed model")
		return
	}
	permitted := false
	for _, p := range md.SharePerms {
		if string(p) == body.Permission {
			permitted = true
			break
		}
	}
	if !permitted {
		writeRouteError(w, http.StatusBadRequest, "not_shareable", body.Model+" does not accept a "+body.Permission+" share")
		return
	}

	// Capping check runs before the recipient lookup, not after — an
	// email-existence probe (registered vs. recipient_not_found) must
	// never be reachable by a caller who can't even read the record
	// named in the request; gating on record access first means that
	// caller always gets the same permission_denied regardless of
	// whether user_email is registered.
	ctx := r.Context()
	if !e.sharerCanReadRecord(ctx, authCtx, tenantCtx, body.Model, body.RecordID) {
		writeRouteError(w, http.StatusForbidden, "permission_denied", "you do not have access to this record")
		return
	}

	recipient, err := e.userStore.GetByEmail(ctx, body.UserEmail)
	if err != nil {
		if errors.Is(err, user.ErrUserNotFound) {
			writeRouteError(w, http.StatusBadRequest, "recipient_not_found", "no user with that email")
			return
		}
		writeRouteError(w, http.StatusInternalServerError, "internal_error", "look up recipient failed")
		return
	}

	sh, err := e.recordSharesStore.Create(ctx, tenantCtx.Slug, body.Model, body.RecordID, recipient.ID, body.Permission, authCtx.UserID, body.ExpiresAt)
	if err != nil {
		writeRouteError(w, http.StatusInternalServerError, "internal_error", "create share failed")
		return
	}

	writeJSON(w, http.StatusCreated, shareToResponse(sh))
}

// dispatchSharesListRoute is GET /_meta/shares' handler (goerp#475) —
// ?model=...&record_id=... lists every non-expired grant on that record.
func (e *Engine) dispatchSharesListRoute(w http.ResponseWriter, r *http.Request) {
	authCtx := authFromContext(r.Context())
	tenantCtx := tenantFromContext(r.Context())
	if authCtx == nil || tenantCtx == nil {
		writeRouteError(w, http.StatusServiceUnavailable, "not_ready", "tenant/auth context not resolved")
		return
	}

	q := r.URL.Query()
	modelName := q.Get("model")
	recordID := q.Get("record_id")
	if modelName == "" || recordID == "" {
		writeRouteError(w, http.StatusBadRequest, "invalid_request", "model and record_id query parameters are required")
		return
	}

	ctx := r.Context()
	if !e.sharerCanReadRecord(ctx, authCtx, tenantCtx, modelName, recordID) {
		writeRouteError(w, http.StatusForbidden, "permission_denied", "you do not have access to this record")
		return
	}

	shares, err := e.recordSharesStore.ListForRecord(ctx, tenantCtx.Slug, modelName, recordID)
	if err != nil {
		writeRouteError(w, http.StatusInternalServerError, "internal_error", "list shares failed")
		return
	}

	out := make([]shareResponse, len(shares))
	for i, sh := range shares {
		out[i] = shareToResponse(&sh)
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": out})
}

// dispatchSharesDeleteRoute is DELETE /_meta/shares/{id}'s handler
// (goerp#475) — revokes a grant. A share is a hard-deleted row (no
// separate revoked flag), so revocation is simply removing it. Gated by
// the same host.orm.read capping check POST/GET use, resolved against
// the target share's own (model, record_id) — revoking a share requires
// the same access a fresh share request against that record would.
func (e *Engine) dispatchSharesDeleteRoute(w http.ResponseWriter, r *http.Request) {
	authCtx := authFromContext(r.Context())
	tenantCtx := tenantFromContext(r.Context())
	if authCtx == nil || tenantCtx == nil {
		writeRouteError(w, http.StatusServiceUnavailable, "not_ready", "tenant/auth context not resolved")
		return
	}

	id := route.ParamsFromContext(r.Context())["id"]
	if id == "" {
		writeRouteError(w, http.StatusBadRequest, "invalid_path_param", "id path parameter is required")
		return
	}

	ctx := r.Context()
	sh, err := e.recordSharesStore.Get(ctx, tenantCtx.Slug, id)
	if err != nil {
		if errors.Is(err, recordshares.ErrNotFound) {
			writeRouteError(w, http.StatusNotFound, "not_found", "share not found")
			return
		}
		writeRouteError(w, http.StatusInternalServerError, "internal_error", "revoke share failed")
		return
	}
	if !e.sharerCanReadRecord(ctx, authCtx, tenantCtx, sh.Model, sh.RecordID) {
		writeRouteError(w, http.StatusForbidden, "permission_denied", "you do not have access to this record")
		return
	}

	if err := e.recordSharesStore.Delete(ctx, tenantCtx.Slug, id); err != nil {
		if errors.Is(err, recordshares.ErrNotFound) {
			writeRouteError(w, http.StatusNotFound, "not_found", "share not found")
			return
		}
		writeRouteError(w, http.StatusInternalServerError, "internal_error", "revoke share failed")
		return
	}

	w.WriteHeader(http.StatusNoContent)
}
