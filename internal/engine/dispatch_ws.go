package engine

import (
	"net/http"
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
		// Unreachable via the real middleware chain — routeAuthMiddleware
		// requires Auth: "required" (registry.go's registration) before
		// this handler is ever reached. Guarded for direct-call
		// testability, matching dispatchPermissionsRoute's identical guard.
		httperr.Write(r.Context(), w, http.StatusServiceUnavailable, "not_ready", "tenant/auth context not resolved")
		return
	}

	conn, err := websocket.Accept(w, r, nil)
	if err != nil {
		// Accept already wrote an HTTP error response to w.
		return
	}
	// Serve's only return is a non-nil error from a failed read — a
	// client-initiated close included, since coder/websocket has no
	// "closed cleanly" signal distinct from an error. The library itself
	// already closes the connection with an appropriate reason on that
	// error (its own documented behavior), so CloseNow here is just
	// resource cleanup, not a second close attempt.
	defer func() { _ = conn.CloseNow() }()

	connID := uuid.New().String()
	_ = e.wsHub.Serve(r.Context(), conn, connID, authCtx.UserID, tenantCtx.TenantID, r.UserAgent())
}
