package engine

import (
	"context"
	"net/http"
	"slices"
	"uuid"

	"github.com/coder/websocket"
	"github.com/djangbahevans/goerp/internal/engine/httperr"
)

// WebSocket upgrades use the standard tenant/auth middleware. Unauthenticated requests
// receive HTTP 401 before a connection exists.
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
	_ = e.wsHub.Serve(r.Context(), conn, connID, authCtx.UserID, tenantCtx.TenantID, r.UserAgent(), func(ctx context.Context, name string) (bool, error) {
		roles, err := e.roleStore.RoleNamesForUser(ctx, tenantCtx.Slug, authCtx.UserID)
		return slices.Contains(roles, name), err
	})
}
