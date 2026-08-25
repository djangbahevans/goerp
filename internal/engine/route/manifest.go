package route

import "time"

type RouteManifest struct {
	Auth        string // "required"|"optional"|"none" — RouteDeclaration.Auth's wire value
	Permissions []string

	RateLimit *RateLimitConfig // nil = use the engine-wide default, not "no limit"

	Model          string // "{module}.{resource}"; "" = no model binding
	ResponseIsList bool

	MaxBodyBytes int64
	RawBody      bool

	Timeout time.Duration

	Streaming  bool
	Websocket  bool
	PathParams map[string]string // param name -> declared kind ("uuid"|"slug"|"int")

	CrudAction string // "get"|"list"|"create"|"update"|"delete"|"preview"|""

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
}

type RateLimitConfig struct {
	Requests      int
	WindowSeconds int
	Scope         string
}
