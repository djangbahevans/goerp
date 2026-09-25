package route

import (
	"time"

	abiv1 "github.com/djangbahevans/goerp/contract/abi/v1"
)

type RouteManifest struct {
	Auth        string // "required"|"optional"|"none" — RouteDeclaration.Auth's wire value
	Permissions []string

	RateLimit *RateLimitConfig // nil = use the engine-wide default, not "no limit"

	Model          string // "{module}.{resource}"; "" = no model binding
	ResponseIsList bool

	// Name is the action name an engine.Action-registered route was given
	// (go-sdk-reference.md §2a) — "" for an EnableOps-auto-generated CRUD
	// route (route_model.go never sets this field) and for a raw,
	// non-model route. A hand-registered override of a reserved CRUD name
	// sets both this and CrudAction to the same value.
	Name string

	MaxBodyBytes int64
	RawBody      bool

	Timeout time.Duration

	Streaming  bool
	Websocket  bool
	PathParams map[string]string // param name -> declared kind ("uuid"|"slug"|"int")

	CrudAction string // "get"|"list"|"create"|"update"|"delete"|"preview"|"pivot"|""

	// EngineNative marks a route the dispatch handler serves without
	// borrowing/invoking a WASM instance — the builtins map lookup
	// (dispatch.go) today, and the dispatchORMRoute branch (goerp#92)
	// once it lands for EnableOps-derived Table/Transient routes. It
	// says nothing about tenant/auth — an EnableOps route is
	// EngineNative and still passes through the full tenant/auth/
	// permission middleware chain like any other module route.
	EngineNative bool

	// EngineBuiltin marks a route that resolves its own tenant/auth
	// entirely inside its own handler and must never reach the standard
	// tenant/auth/MFA/permission middleware chain — set only on the
	// fixed set of engine-builtin infra routes registerBuiltinRoutes
	// registers (registry.go), the auth-internals.md §9 "Route classes"
	// B/C/D routes (plus /_health/_ready, which need neither tenant nor
	// auth at all).
	EngineBuiltin bool

	StorageBackend string // "table"|"transient"|"virtual"

	// RequestType and ResponseType are the route's engine.Body/engine.Returns
	// declarations, served in /_meta/schema for goerp codegen only.
	RequestType  *abiv1.TypeDesc
	ResponseType *abiv1.TypeDesc

	// Workflow carries a .Workflow()-declared transition's own from/to/
	// field/condition — set only when CrudAction is "workflow_transition".
	// dispatchORMWorkflowTransition reads this to validate the record's
	// current state and drive the write; nothing else on RouteManifest
	// names the specific field a workflow transition governs.
	Workflow *WorkflowManifest
}

// WorkflowManifest is one .Workflow()-declared transition's dispatch-time
// data — the RouteManifest.Workflow companion to Name (the transition's
// action name) and Permissions (its .Requires() permission, if any).
type WorkflowManifest struct {
	Field     string // the Selection field this transition governs
	From      string
	To        string
	Condition string // raw domain-expression string; not evaluated (goerp#864)
}

type RateLimitConfig struct {
	Requests      int
	WindowSeconds int
	Scope         string
}
