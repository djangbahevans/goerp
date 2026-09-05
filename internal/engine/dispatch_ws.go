package engine

import (
	"net/http"
	"uuid"

	"github.com/coder/websocket"
)

// dispatchWSRoute is GET /_ws's handler (goerp#616) — registered
// EngineNative but not EngineBuiltin (registry.go), so it rides the
// standard tenant/auth/permission middleware chain like any other Class A
// route (auth-internals.md §9) rather than resolving its own identity.
// Cookie-based session auth therefore happens before this handler ever
// runs: an unauthenticated upgrade attempt is rejected with a plain HTTP
// 401 by routeAuthMiddleware and never reaches here at all — there is no
// scenario in which this handler itself needs to reject an upgrade with
// close code 4001, since a connection only exists here once auth already
// succeeded. (4001 remains shell-architecture.md's documented convention
// for a session becoming invalid *during* an already-open connection —
// this ticket doesn't yet have a mechanism to detect that, since
// authcheck.AuthContext carries no expiry to watch; left as follow-up
// scope.)
func (e *Engine) dispatchWSRoute(w http.ResponseWriter, r *http.Request) {
	authCtx := authFromContext(r.Context())
	tenantCtx := tenantFromContext(r.Context())
	if authCtx == nil || tenantCtx == nil {
		// Unreachable via the real middleware chain — routeAuthMiddleware
		// requires Auth: "required" (registry.go's registration) before
		// this handler is ever reached. Guarded for direct-call
		// testability, matching dispatchPermissionsRoute's identical guard.
		writeRouteError(w, http.StatusServiceUnavailable, "not_ready", "tenant/auth context not resolved")
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
