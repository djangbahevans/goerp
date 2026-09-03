package engine

import (
	"time"

	"github.com/djangbahevans/goerp/internal/engine/permission"
)

// EngineRequest's msgpack tags must match sdk/go/engine.Request's own
// tags field-for-field — invokeHandler marshals this type and the module
// decodes the bytes straight into that one, so any mismatch (or missing
// tag, which vmihailenco/msgpack falls back to encoding as the bare,
// capitalized Go field name for) silently decodes to a zero value inside
// the module rather than failing the call outright.
type EngineRequest struct {
	ID          string            `msgpack:"id"`
	Method      string            `msgpack:"method"`
	Path        string            `msgpack:"path"`
	PathParams  map[string]string `msgpack:"params"`
	QueryParams map[string]string `msgpack:"query"`
	Headers     map[string]string `msgpack:"headers"`
	// Body is the request's raw, unparsed bytes — sdk/go/engine.Request.Body
	// is likewise []byte, decoded via the module's own req.ParseJSON/
	// RawBody rather than pre-decoded engine-side, so a module using
	// RawBody() (e.g. webhook signature verification) sees the exact
	// bytes the client sent.
	Body   []byte `msgpack:"body"`
	UserID string `msgpack:"user_id"`
	// PermissionSet is the caller's resolved permission bitfield
	// (authcheck.AuthContext.PermissionSet) — threaded through to the
	// module context so host functions can evaluate field-security rules
	// without re-querying permcache (auth-internals.md §13). Has no
	// sdk/go/engine.Request counterpart — a module reads its caller's
	// permissions via host.authz, not off the request itself — so it's
	// left untagged; the module's decode simply ignores the extra key.
	PermissionSet permission.PermissionBitfield
	TenantID      string    `msgpack:"tenant_id"`
	TenantSlug    string    `msgpack:"tenant_slug"`
	Locale        string    `msgpack:"locale"`
	Timezone      string    `msgpack:"timezone"`
	Currency      string    `msgpack:"currency"`
	Direction     string    `msgpack:"direction"`
	TraceID       string    `msgpack:"trace_id"`
	RequestAt     time.Time `msgpack:"requested_at"`
}
