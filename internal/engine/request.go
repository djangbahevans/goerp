package engine

import (
	abi "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/djangbahevans/goerp/internal/engine/permission"
)

// EngineRequest is the request invokeHandler marshals for a module's
// handle_request export: the wire request plus the caller's resolved
// permission bitfield (authcheck.AuthContext.PermissionSet), threaded
// through to the module context so host functions can evaluate
// field-security rules without re-querying permcache (auth-internals.md
// §13). PermissionSet has no counterpart in the wire request — a module
// reads its caller's permissions via host.authz — so it is left untagged
// and the module's decode ignores the extra key.
type EngineRequest struct {
	abi.Request
	PermissionSet permission.PermissionBitfield
}
