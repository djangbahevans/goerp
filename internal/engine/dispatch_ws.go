package engine

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"uuid"

	"github.com/coder/websocket"
	"github.com/djangbahevans/goerp/internal/engine/auth/authme"
	"github.com/djangbahevans/goerp/internal/engine/httperr"
	"github.com/djangbahevans/goerp/internal/engine/module"
	"github.com/djangbahevans/goerp/internal/engine/registry"
	tenantresolve "github.com/djangbahevans/goerp/internal/engine/tenant/resolve"
	"github.com/djangbahevans/goerp/internal/engine/user"
	"github.com/djangbahevans/goerp/internal/engine/ws"
)

func (e *Engine) dispatchWSRoute(w http.ResponseWriter, r *http.Request) {
	authCtx := authFromContext(r.Context())
	tenantCtx := tenantFromContext(r.Context())
	if authCtx == nil || tenantCtx == nil {
		httperr.Write(r.Context(), w, http.StatusServiceUnavailable, "not_ready", "tenant/auth context not resolved")
		return
	}

	conn, err := websocket.Accept(w, r, nil)
	if err != nil {
		// Accept already wrote an HTTP error response to w.
		return
	}
	defer func() { _ = conn.CloseNow() }()

	connID := uuid.New().String()
	_ = e.wsHub.Serve(r.Context(), conn, connID, authCtx.UserID, tenantCtx.TenantID, r.UserAgent(), ws.AccessChecks{
		Role: func(ctx context.Context, name string) (bool, error) {
			roles, err := e.roleStore.RoleNamesForUser(ctx, tenantCtx.Slug, authCtx.UserID)
			return slices.Contains(roles, name), err
		},
		ModuleEnabled: func(ctx context.Context, name string) (bool, error) {
			return e.wsModuleEnabled(ctx, tenantCtx.TenantID, name)
		},
		ReadResource: func(ctx context.Context, mod, resource string) (bool, error) {
			return e.wsCanReadResource(ctx, tenantCtx, authCtx.UserID, mod, resource)
		},
		ReadRecord: func(ctx context.Context, resource, id string) (bool, error) {
			if e.moduleRegistry == nil {
				return false, nil
			}
			if _, registered := wsResource(e.moduleRegistry.Snapshot(), resource); !registered {
				return false, nil
			}
			bits, member, err := e.memberPermissionSet(ctx, tenantCtx.Slug, authCtx.UserID)
			if err != nil || !member {
				return false, err
			}
			records, err := e.readRecordsAsErr(ctx, tenantCtx, authCtx.UserID, bits, resource, []string{id}, nil)
			return len(records) > 0, err
		},
		RecordEligible: func(ctx context.Context, resource string) (bool, error) {
			return e.wsRecordEligible(ctx, tenantCtx, authCtx.UserID, resource)
		},
		PresenceProfile: func(ctx context.Context) (ws.PresenceProfile, error) {
			return e.wsPresenceProfile(ctx, tenantCtx.Slug, authCtx.UserID)
		},
	})
}

func (e *Engine) wsPresenceProfile(ctx context.Context, tenantSlug, userID string) (ws.PresenceProfile, error) {
	usr, err := e.userStore.GetByID(ctx, userID)
	if err != nil {
		return ws.PresenceProfile{}, fmt.Errorf("read presence user: %w", err)
	}
	result := ws.PresenceProfile{Name: usr.Email}
	profile, err := e.userStore.GetProfile(ctx, userID)
	if errors.Is(err, user.ErrProfileNotFound) {
		return result, nil
	}
	if err != nil {
		return ws.PresenceProfile{}, fmt.Errorf("read presence profile: %w", err)
	}
	if name := profile.DisplayName(); name != nil {
		result.Name = *name
	}
	if profile.AvatarFileID != nil {
		result.AvatarURL = authme.AvatarURL(ctx, e.filesStore, e.storageBackend, tenantSlug, userID, *profile.AvatarFileID)
	}

	return result, nil
}

func (e *Engine) wsRecordEligible(ctx context.Context, tenantCtx *tenantresolve.TenantContext, userID, resource string) (bool, error) {
	if e.moduleRegistry == nil {
		return false, nil
	}
	if _, registered := wsResource(e.moduleRegistry.Snapshot(), resource); !registered {
		return false, nil
	}
	owner, _, _ := strings.Cut(resource, ".")
	enabled, err := e.wsModuleEnabled(ctx, tenantCtx.TenantID, owner)
	if err != nil || !enabled {
		return false, err
	}
	_, member, err := e.memberPermissionSet(ctx, tenantCtx.Slug, userID)
	return member, err
}

func (e *Engine) wsModuleEnabled(ctx context.Context, tenantID, name string) (bool, error) {
	if e.moduleRegistry == nil || e.tenantResolver == nil {
		return false, nil
	}
	snap := e.moduleRegistry.Snapshot()
	if snap == nil {
		return false, nil
	}
	mod := snap.Modules()[name]
	if mod == nil || mod.Status != module.StatusReady {
		return false, nil
	}
	ents, err := e.tenantResolver.LoadCurrentEntitlements(ctx, tenantID)
	return err == nil && ents.ModuleEnabled(name), err
}

func (e *Engine) wsCanReadResource(ctx context.Context, tenantCtx *tenantresolve.TenantContext, userID, mod, resource string) (bool, error) {
	owner, _, _ := strings.Cut(resource, ".")
	if owner != mod || e.moduleRegistry == nil {
		return false, nil
	}
	snap := e.moduleRegistry.Snapshot()
	list, registered := wsResource(snap, resource)
	if !registered || list == nil {
		return false, nil
	}
	bits, member, err := e.memberPermissionSet(ctx, tenantCtx.Slug, userID)
	if err != nil || !member {
		return false, err
	}
	for _, name := range list.Permissions {
		idx, ok := snap.PermissionRegistry().Index(name)
		if !ok || !bits.Has(idx) {
			return false, nil
		}
	}
	return true, nil
}

// Resource admission follows the shell's list/get CRUD registry, including named overrides.
func wsResource(snap *registry.RegistrySnapshot, resource string) (list *registry.SchemaRoute, registered bool) {
	if snap == nil || snap.SchemaResponse() == nil {
		return nil, false
	}
	owner, _, _ := strings.Cut(resource, ".")
	mod := snap.SchemaResponse().Modules[owner]
	if mod == nil {
		return nil, false
	}
	for i := range mod.Routes {
		rt := &mod.Routes[i]
		if rt.Model != resource || (!rt.EngineNative && rt.Name != rt.CrudAction) {
			continue
		}
		switch rt.CrudAction {
		case "list":
			list, registered = rt, true
		case "get":
			registered = true
		}
	}
	return list, registered
}
