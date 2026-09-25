package abi

// RateLimitScope names what a route's rate limit is counted against.
type RateLimitScope string

const (
	RateLimitScopeUser   RateLimitScope = "user"
	RateLimitScopeTenant RateLimitScope = "tenant"
	RateLimitScopeIP     RateLimitScope = "ip"
	RateLimitScopeAPIKey RateLimitScope = "api_key"
)

// RouteDeclaration is one entry of the table a module's get_routes export
// returns.
type RouteDeclaration struct {
	Method       string         `msgpack:"method"`
	Path         string         `msgpack:"path"`
	Auth         string         `msgpack:"auth"`
	Permissions  []string       `msgpack:"permissions"`
	RateLimit    *RateLimitDecl `msgpack:"rate_limit,omitempty"`
	MaxBodyBytes int            `msgpack:"max_body_bytes"`
	TimeoutMs    int            `msgpack:"timeout_ms"`
	Streaming    bool           `msgpack:"streaming"`
	Websocket    bool           `msgpack:"websocket"`
	RawBody      bool           `msgpack:"raw_body"`
	Model        string         `msgpack:"model,omitempty"`
	Name         string         `msgpack:"name,omitempty"`

	// Scope is an engine.Action route's declared scope ("record" or
	// "collection"); empty selects the default. On an action route, Method is
	// the declared method of a custom action and empty otherwise.
	Scope string `msgpack:"scope,omitempty"`

	CRUDAction     string            `msgpack:"crud_action,omitempty"`
	ResponseIsList bool              `msgpack:"response_is_list"`
	Embedded       []EmbeddedDecl    `msgpack:"embedded,omitempty"`
	PathParams     map[string]string `msgpack:"path_params,omitempty"`

	// RequestType and ResponseType describe the route's JSON request body
	// and response data (engine.Body, engine.Returns), for goerp codegen.
	RequestType  *TypeDesc `msgpack:"request_type,omitempty"`
	ResponseType *TypeDesc `msgpack:"response_type,omitempty"`
}

// TypeKind is the JSON shape a TypeDesc describes.
type TypeKind string

const (
	TypeKindString  TypeKind = "string"
	TypeKindNumber  TypeKind = "number"
	TypeKindBoolean TypeKind = "boolean"
	TypeKindUnknown TypeKind = "unknown"
	TypeKindArray   TypeKind = "array"
	TypeKindRecord  TypeKind = "record"
	TypeKindObject  TypeKind = "object"
)

// TypeDesc describes a Go type's JSON encoding for goerp codegen.
type TypeDesc struct {
	Kind     TypeKind    `msgpack:"kind"`
	Name     string      `msgpack:"name,omitempty"`     // Go type name of a named struct
	Elem     *TypeDesc   `msgpack:"elem,omitempty"`     // array and record element
	Fields   []FieldDesc `msgpack:"fields,omitempty"`   // object fields, in declaration order
	Nullable bool        `msgpack:"nullable,omitempty"` // declared as a pointer
}

// FieldDesc is one field of an object TypeDesc.
type FieldDesc struct {
	Name     string   `msgpack:"name"` // json tag name
	Type     TypeDesc `msgpack:"type"`
	Optional bool     `msgpack:"optional,omitempty"` // omitempty or omitzero
}

// RateLimitDecl is a route's rate limit.
type RateLimitDecl struct {
	Requests      int            `msgpack:"requests"`
	WindowSeconds int            `msgpack:"window_seconds"`
	Scope         RateLimitScope `msgpack:"scope"`
}

// EmbeddedDecl declares a related resource embedded in a route's response.
type EmbeddedDecl struct {
	Field    string `msgpack:"field"`
	Resource string `msgpack:"resource"`
	IsList   bool   `msgpack:"is_list"`
}
