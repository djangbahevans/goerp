package engine

import (
	"time"

	"github.com/djangbahevans/goerp/internal/engine/permission"
)

type EngineRequest struct {
	ID          string
	Method      string
	Path        string
	PathParams  map[string]string
	QueryParams map[string]string
	Headers     map[string]string
	Body        map[string]any
	UserID      string
	// PermissionSet is the caller's resolved permission bitfield
	// (authcheck.AuthContext.PermissionSet) — threaded through to the
	// module context so host functions can evaluate field-security rules
	// without re-querying permcache (auth-internals.md §13).
	PermissionSet permission.PermissionBitfield
	TenantID      string
	TenantSlug    string
	Locale        string
	Timezone      time.Location
	Currency      string
	Direction     string
	TraceID       string
	RequestAt     time.Time
}
