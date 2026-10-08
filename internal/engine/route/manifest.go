package route

import (
	"time"

	abiv1 "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/djangbahevans/goerp/sdk/go/model"
)

type RouteManifest struct {
	Auth        string // "required"|"optional"|"none" — RouteDeclaration.Auth's wire value
	Permissions []string

	RateLimit *RateLimitConfig // nil = use the engine-wide default, not "no limit"

	Model          string // "{module}.{resource}"; "" = no model binding
	ResponseIsList bool

	// Reserved CRUD overrides set both Name and CrudAction; derived CRUD routes leave Name empty.
	Name string

	MaxBodyBytes int64
	RawBody      bool

	Timeout time.Duration

	Streaming  bool
	Websocket  bool
	PathParams map[string]string // param name -> declared kind ("uuid"|"slug"|"int")

	CrudAction string // "get"|"list"|"create"|"update"|"delete"|"preview"|"pivot"|""

	// EngineNative selects Go dispatch without a WASM instance. Module-owned native routes
	// still pass through tenant/auth/permission middleware.
	EngineNative bool

	// Builtin routes resolve their own tenant and authentication context and must bypass
	// the module middleware chain.
	EngineBuiltin bool

	// OwnRateLimit marks a route whose handler enforces its own limit, so the
	// engine's per-IP rate limit middleware skips it.
	OwnRateLimit bool

	StorageBackend string // "table"|"transient"|"virtual"

	// RequestType and ResponseType are the route's engine.Body/engine.Returns
	// declarations, served in /_meta/schema for goerp codegen only.
	RequestType  *abiv1.TypeDesc
	ResponseType *abiv1.TypeDesc

	Workflow *WorkflowManifest
}

type WorkflowManifest struct {
	Field     string
	From      string
	To        string
	Condition string // Raw domain expression; not evaluated by the server.
	Event     *model.LifecycleEvent
}

type RateLimitConfig struct {
	Requests      int
	WindowSeconds int
	Scope         string
}
