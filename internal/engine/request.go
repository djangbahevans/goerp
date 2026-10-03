package engine

import (
	abiv1 "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/djangbahevans/goerp/internal/engine/permission"
)

// EngineRequest carries the wire request and resolved authorization state.
// ContactID and RolesLive stay on the host side for ABAC session variables;
// modules read permissions through host.authz.
type EngineRequest struct {
	abiv1.Request
	PermissionSet permission.PermissionBitfield
	ContactID     string   `msgpack:"-"`
	RolesLive     []string `msgpack:"-"`
}
