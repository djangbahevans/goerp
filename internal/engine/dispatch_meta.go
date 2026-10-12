package engine

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"time"

	abiv1 "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/djangbahevans/goerp/internal/engine/auth/authcheck"
	"github.com/djangbahevans/goerp/internal/engine/httperr"
	"github.com/djangbahevans/goerp/internal/engine/module"
	"github.com/djangbahevans/goerp/internal/engine/permission"
	"github.com/djangbahevans/goerp/internal/engine/recordshares"
	"github.com/djangbahevans/goerp/internal/engine/savedfilters"
	tenantresolve "github.com/djangbahevans/goerp/internal/engine/tenant/resolve"
	"github.com/djangbahevans/goerp/internal/engine/user"
	"github.com/djangbahevans/goerp/internal/engine/wasm"
	sdkmodel "github.com/djangbahevans/goerp/sdk/go/model"
	"github.com/rs/zerolog/log"
)

type metaPermissionsResponse struct {
	Permissions    []string                          `json:"permissions"`
	FieldAccess    map[string]map[string]fieldAccess `json:"field_access"`
	ModulesEnabled []string                          `json:"modules_enabled"`
}

type fieldAccess struct {
	Read  bool `json:"read"`
	Write bool `json:"write"`
}

func (e *Engine) dispatchPermissionsRoute(w http.ResponseWriter, r *http.Request) {
	authCtx := authFromContext(r.Context())
	tenantCtx := tenantFromContext(r.Context())
	if authCtx == nil || tenantCtx == nil {
		httperr.Write(r.Context(), w, http.StatusServiceUnavailable, "not_ready", "tenant/auth context not resolved")
		return
	}

	snap := e.moduleRegistry.Snapshot()
	if snap == nil {
		httperr.Write(r.Context(), w, http.StatusServiceUnavailable, "not_ready", "engine has not finished starting")
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
	slices.Sort(modulesEnabled)

	writeJSON(r.Context(), w, http.StatusOK, metaPermissionsResponse{
		Permissions:    permissions,
		FieldAccess:    fieldAccessMap,
		ModulesEnabled: modulesEnabled,
	})
}

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

type shareCreateRequest struct {
	Model      string     `json:"model"`
	RecordID   string     `json:"record_id"`
	UserEmail  string     `json:"user_email"`
	Permission string     `json:"permission"`
	ExpiresAt  *time.Time `json:"expires_at,omitempty"`
}

type shareResponse struct {
	ID               string `json:"id"`
	Model            string `json:"model"`
	RecordID         string `json:"record_id"`
	SharedWithUserID string `json:"shared_with_user_id"`
	// SharedWithEmail is empty when the recipient's user record no longer
	// exists.
	SharedWithEmail string     `json:"shared_with_email"`
	Permission      string     `json:"permission"`
	SharedBy        string     `json:"shared_by"`
	CreatedAt       time.Time  `json:"created_at"`
	ExpiresAt       *time.Time `json:"expires_at,omitempty"`
}

func shareToResponse(sh *recordshares.Share, recipientEmail string) shareResponse {
	return shareResponse{
		ID:               sh.ID,
		Model:            sh.Model,
		RecordID:         sh.RecordID,
		SharedWithUserID: sh.SharedWithUserID,
		SharedWithEmail:  recipientEmail,
		Permission:       sh.Permission,
		SharedBy:         sh.SharedBy,
		CreatedAt:        sh.CreatedAt,
		ExpiresAt:        sh.ExpiresAt,
	}
}

// Record sharing and activity operations require current read access to the record. An
// unresolved model or host error denies access.
func (e *Engine) callerCanReadRecord(ctx context.Context, authCtx *authcheck.AuthContext, tenantCtx *tenantresolve.TenantContext, modelName, recordID string) bool {
	records, ok := e.readRecordsAs(ctx, tenantCtx, authCtx.UserID, authCtx.PermissionSet, modelName, []string{recordID}, nil)
	return ok && len(records) > 0
}

// userCanReadRecord reports whether userID, someone other than the caller,
// can read recordID: they must be an active user holding an unexpired role
// in the tenant, and host.orm.read with their permissions must return the
// record (record-activity.md §7 "Reading as another user").
func (e *Engine) userCanReadRecord(ctx context.Context, tenantCtx *tenantresolve.TenantContext, userID, modelName, recordID string) (bool, error) {
	permSet, member, err := e.memberPermissionSet(ctx, tenantCtx.Slug, userID)
	if err != nil || !member {
		return false, err
	}
	records, ok := e.readRecordsAs(ctx, tenantCtx, userID, permSet, modelName, []string{recordID}, nil)
	return ok && len(records) > 0, nil
}

// memberPermissionSet returns userID's permission set in the tenant, from
// their live roles. member is false for a missing or inactive user, or one
// with no unexpired role in the tenant.
func (e *Engine) memberPermissionSet(ctx context.Context, tenantSlug, userID string) (permSet permission.PermissionBitfield, member bool, err error) {
	u, err := e.userStore.GetByID(ctx, userID)
	if errors.Is(err, user.ErrUserNotFound) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("load user: %w", err)
	}
	if u.Status != user.StatusActive {
		return nil, false, nil
	}
	roleIDs, err := e.roleStore.RoleIDsForUser(ctx, tenantSlug, userID)
	if err != nil {
		return nil, false, fmt.Errorf("load role ids: %w", err)
	}
	if len(roleIDs) == 0 {
		return nil, false, nil
	}
	for _, roleID := range roleIDs {
		if bits, ok := e.rolePermissionMap.Lookup(roleID); ok {
			permSet.Or(bits)
		}
	}
	return permSet, true, nil
}

// readRecordsAs reads ids of modelName through host.orm.read as userID
// holding permSet, so role permissions, the model's ABAC condition and
// record shares decide which come back. fields nil reads every field. ok
// is false on an unresolvable model or any host error.
func (e *Engine) readRecordsAs(ctx context.Context, tenantCtx *tenantresolve.TenantContext, userID string, permSet permission.PermissionBitfield, modelName string, ids, fields []string) (records []map[string]any, ok bool) {
	records, err := e.readRecordsAsErr(ctx, tenantCtx, userID, permSet, modelName, ids, fields)
	return records, err == nil
}

var errReadModelNotFound = errors.New("model not found")

// readRecordsAsErr is readRecordsAs reporting why a read failed: an error
// wrapping errReadModelNotFound for a model no loaded module declares,
// any other for a registry not yet loaded or a host error. A record the
// reader can't see is left out of records, not an error.
func (e *Engine) readRecordsAsErr(ctx context.Context, tenantCtx *tenantresolve.TenantContext, userID string, permSet permission.PermissionBitfield, modelName string, ids, fields []string) ([]map[string]any, error) {
	snap := e.moduleRegistry.Snapshot()
	if snap == nil {
		return nil, errors.New("module registry not loaded")
	}
	_, mod, _, found := snap.ModelByName(modelName)
	if !found {
		return nil, fmt.Errorf("%w: %q", errReadModelNotFound, modelName)
	}

	actor, err := e.roleStore.ResolveActingUser(ctx, tenantCtx.Slug, userID)
	if err != nil {
		return nil, fmt.Errorf("resolve record reader: %w", err)
	}
	traceID := httperr.TraceIDFromContext(ctx)
	modCtx := e.newModuleContext(ctx, EngineRequest{
		ID:            requestIDFromContext(ctx),
		UserID:        userID,
		TenantID:      tenantCtx.TenantID,
		TenantSlug:    tenantCtx.Slug,
		TraceID:       traceID,
		PermissionSet: permSet,
		ContactID:     actor.ContactID,
		RolesLive:     actor.Roles,
	}, mod)
	defer modCtx.RollbackAll()

	readOut, hostErr := wasm.ORMRead(ctx, e.primaryDB, e.cacheClient, modCtx, abiv1.ORMReadInput{
		Model:  modelName,
		IDs:    ids,
		Fields: fields,
	})
	if hostErr != nil {
		if hostErr.Code == abiv1.ErrCodeModelNotFound {
			return nil, fmt.Errorf("%w: %s", errReadModelNotFound, hostErr.Message)
		}
		return nil, fmt.Errorf("read %s: %s: %s", modelName, hostErr.Code, hostErr.Message)
	}
	return readOut.Records, nil
}

func (e *Engine) sharesResource() engineResource[shareResponse] {
	emails := map[string]string{}
	res := engineResource[shareResponse]{
		table:       "record_shares",
		accessMode:  ownRowsOnly,
		ownerColumn: "shared_by",
		name:        "share",
		orderBy:     "created_at DESC",
		listWhere:   "(expires_at IS NULL OR expires_at > NOW())",
		filters: func(req resourceRequest) ([]resourceFilter, *resourceError) {
			modelName := req.r.URL.Query().Get("model")
			recordID := req.r.URL.Query().Get("record_id")
			if modelName == "" || recordID == "" {
				return nil, invalidResourceRequest("model and record_id query parameters are required")
			}
			if !e.callerCanReadRecord(req.r.Context(), req.auth, req.tenant, modelName, recordID) {
				return nil, resourceDenied("you do not have access to this record")
			}
			return []resourceFilter{{"model", modelName}, {"record_id", recordID}}, nil
		},
		beforeDelete: func(req resourceRequest, sh shareResponse) *resourceError {
			if !e.callerCanReadRecord(req.r.Context(), req.auth, req.tenant, sh.Model, sh.RecordID) {
				return resourceDenied("you do not have access to this record")
			}
			return nil
		},
		decorate: func(req resourceRequest, sh *shareResponse) {
			email, seen := emails[sh.SharedWithUserID]
			if !seen {
				email = e.recipientEmail(req.r.Context(), sh.SharedWithUserID)
				emails[sh.SharedWithUserID] = email
			}
			sh.SharedWithEmail = email
		},
	}

	return res.withCreate(e.createShare)
}

func (e *Engine) createShare(req resourceRequest, body shareCreateRequest) (shareResponse, int, *resourceError) {
	if body.Model == "" || body.RecordID == "" || body.UserEmail == "" {
		return shareResponse{}, 0, invalidResourceRequest("model, record_id, and user_email are required")
	}
	if body.Permission != string(sdkmodel.ReadShare) && body.Permission != string(sdkmodel.WriteShare) {
		return shareResponse{}, 0, invalidResourceRequest(`permission must be "read" or "write"`)
	}
	if body.ExpiresAt != nil && !body.ExpiresAt.After(time.Now()) {
		return shareResponse{}, 0, invalidResourceRequest("expires_at must be in the future")
	}

	snap := e.moduleRegistry.Snapshot()
	if snap == nil {
		return shareResponse{}, 0, &resourceError{http.StatusServiceUnavailable, "not_ready", "engine has not finished starting"}
	}
	_, _, md, ok := snap.ModelByName(body.Model)
	if !ok {
		return shareResponse{}, 0, &resourceError{http.StatusBadRequest, "model_not_found", "unknown model: " + body.Model}
	}
	if !md.Shareable {
		return shareResponse{}, 0, &resourceError{http.StatusBadRequest, "not_shareable", body.Model + " is not declared .Shareable()"}
	}
	if md.Backend != "" {
		return shareResponse{}, 0, &resourceError{http.StatusBadRequest, "not_shareable", body.Model + " is " + string(md.Backend) + "-backed; .Shareable() requires a Postgres-backed model"}
	}
	if !slices.Contains(md.SharePerms, sdkmodel.SharePermission(body.Permission)) {
		return shareResponse{}, 0, &resourceError{http.StatusBadRequest, "not_shareable", body.Model + " does not accept a " + body.Permission + " share"}
	}

	// Record access precedes email lookup to prevent probing registered recipients.
	ctx := req.r.Context()
	if !e.callerCanReadRecord(ctx, req.auth, req.tenant, body.Model, body.RecordID) {
		return shareResponse{}, 0, &resourceError{http.StatusForbidden, "permission_denied", "you do not have access to this record"}
	}

	recipient, err := e.userStore.GetByEmail(ctx, body.UserEmail)
	if err != nil {
		if errors.Is(err, user.ErrUserNotFound) {
			return shareResponse{}, 0, &resourceError{http.StatusBadRequest, "recipient_not_found", "no user with that email"}
		}
		return shareResponse{}, 0, &resourceError{http.StatusInternalServerError, "internal_error", "look up recipient failed"}
	}

	sh, created, err := e.recordSharesStore.GrantOwned(ctx, req.tenant.Slug, body.Model, body.RecordID, recipient.ID, body.Permission, req.auth.UserID, body.ExpiresAt)
	if errors.Is(err, recordshares.ErrNotOwner) {
		return shareResponse{}, 0, resourceDenied("you do not own this share")
	}
	if err != nil {
		return shareResponse{}, 0, &resourceError{http.StatusInternalServerError, "internal_error", "create share failed"}
	}

	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	return shareToResponse(sh, recipient.Email), status, nil
}

// recipientEmail resolves a share recipient's email, or "" when the user
// record no longer exists or the lookup fails — a share listing degrades to
// an unnamed recipient rather than failing whole.
func (e *Engine) recipientEmail(ctx context.Context, userID string) string {
	u, err := e.userStore.GetByID(ctx, userID)
	if err != nil {
		if !errors.Is(err, user.ErrUserNotFound) {
			log.Warn().Err(err).Str("user_id", userID).Msg("dispatch shares: recipient lookup failed, omitting email")
		}
		return ""
	}
	if u.Status == user.StatusDeleted {
		return ""
	}
	return u.Email
}

func (e *Engine) dispatchSharesCreateRoute(w http.ResponseWriter, r *http.Request) {
	e.sharesResource().serve(e, w, r)
}

func (e *Engine) dispatchSharesListRoute(w http.ResponseWriter, r *http.Request) {
	e.sharesResource().serve(e, w, r)
}

func (e *Engine) dispatchSharesDeleteRoute(w http.ResponseWriter, r *http.Request) {
	e.sharesResource().serve(e, w, r)
}

// A pointer distinguishes an explicit empty bookmark from an omitted query string.
type savedFilterCreateRequest struct {
	ViewName    string  `json:"view_name"`
	Label       string  `json:"label"`
	QueryString *string `json:"query_string"`
	IsDefault   bool    `json:"is_default"`
}

type savedFilterUpdateRequest struct {
	Label     *string `json:"label"`
	IsDefault *bool   `json:"is_default"`
}

type savedFilterResponse struct {
	ID          string `json:"id"`
	ViewName    string `json:"view_name"`
	Label       string `json:"label"`
	QueryString string `json:"query_string"`
	IsDefault   bool   `json:"is_default"`
}

func savedFilterToResponse(sf *savedfilters.SavedFilter) savedFilterResponse {
	return savedFilterResponse{
		ID:          sf.ID,
		ViewName:    sf.ViewName,
		Label:       sf.Label,
		QueryString: sf.QueryString,
		IsDefault:   sf.IsDefault,
	}
}

func (e *Engine) savedFiltersResource() engineResource[savedFilterResponse] {
	res := engineResource[savedFilterResponse]{
		table:       "saved_filters",
		accessMode:  ownRowsOnly,
		ownerColumn: "user_id",
		name:        "saved filter",
		orderBy:     "created_at DESC",
		filters:     viewResourceFilters,
	}

	return res.withCreate(func(req resourceRequest, body savedFilterCreateRequest) (savedFilterResponse, int, *resourceError) {
		if body.ViewName == "" || body.Label == "" || body.QueryString == nil {
			return savedFilterResponse{}, 0, invalidResourceRequest("view_name, label, and query_string are required")
		}
		sf, err := e.savedFiltersStore.Create(req.r.Context(), req.tenant.Slug, req.auth.UserID, body.ViewName, body.Label, *body.QueryString, body.IsDefault)
		if err != nil {
			return savedFilterResponse{}, 0, res.internalFailure("create")
		}
		return savedFilterToResponse(sf), http.StatusCreated, nil
	}).withUpdate(func(req resourceRequest, id string, body savedFilterUpdateRequest) (savedFilterResponse, int, *resourceError) {
		sf, err := e.savedFiltersStore.Update(req.r.Context(), req.tenant.Slug, id, body.Label, body.IsDefault)
		if errors.Is(err, savedfilters.ErrNotFound) {
			return savedFilterResponse{}, 0, res.notFound()
		}
		if err != nil {
			return savedFilterResponse{}, 0, res.internalFailure("update")
		}
		return savedFilterToResponse(sf), http.StatusOK, nil
	})
}

func (e *Engine) dispatchSavedFiltersCreateRoute(w http.ResponseWriter, r *http.Request) {
	e.savedFiltersResource().serve(e, w, r)
}

func (e *Engine) dispatchSavedFiltersListRoute(w http.ResponseWriter, r *http.Request) {
	e.savedFiltersResource().serve(e, w, r)
}

func (e *Engine) dispatchSavedFiltersUpdateRoute(w http.ResponseWriter, r *http.Request) {
	e.savedFiltersResource().serve(e, w, r)
}

func (e *Engine) dispatchSavedFiltersDeleteRoute(w http.ResponseWriter, r *http.Request) {
	e.savedFiltersResource().serve(e, w, r)
}

// Schema discovery exposes the full declared API surface to authenticated callers. The
// response is precomputed for each registry snapshot.
func (e *Engine) dispatchSchemaRoute(w http.ResponseWriter, r *http.Request) {
	authCtx := authFromContext(r.Context())
	tenantCtx := tenantFromContext(r.Context())
	if authCtx == nil || tenantCtx == nil {
		httperr.Write(r.Context(), w, http.StatusServiceUnavailable, "not_ready", "tenant/auth context not resolved")
		return
	}

	snap := e.moduleRegistry.Snapshot()
	if snap == nil {
		httperr.Write(r.Context(), w, http.StatusServiceUnavailable, "not_ready", "engine has not finished starting")
		return
	}

	writeJSON(r.Context(), w, http.StatusOK, snap.SchemaResponse())
}
