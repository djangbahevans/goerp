package engine

type RateLimitScope string

const (
	PerUser   RateLimitScope = "user"
	PerTenant RateLimitScope = "tenant"
	PerIP     RateLimitScope = "ip"
	PerAPIKey RateLimitScope = "api_key"
)

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

	CRUDAction     string                    `msgpack:"crud_action,omitempty"`
	ResponseIsList bool                      `msgpack:"response_is_list"`
	Embedded       []EmbeddedDecl            `msgpack:"embedded,omitempty"`
	PathParams     map[string]string         `msgpack:"path_params,omitempty"`
	QueryParams    map[string]QueryParamDecl `msgpack:"query_params,omitempty"`
}

// QueryParamDecl documents one query-string parameter a route accepts —
// purely descriptive metadata for /_meta/schema and goerp codegen's
// generated client types; the engine neither validates nor enforces it at
// request time (req.QueryParam and friends, sdk/go/engine.Request, remain
// the only runtime-facing query-string API).
type QueryParamDecl struct {
	Type    string   `msgpack:"type"`
	Enum    []string `msgpack:"enum,omitempty"`
	Default any      `msgpack:"default,omitempty"`
	Max     *int     `msgpack:"max,omitempty"`
}

type RateLimitDecl struct {
	Requests      int            `msgpack:"requests"`
	WindowSeconds int            `msgpack:"window_seconds"`
	Scope         RateLimitScope `msgpack:"scope"`
}

type EmbeddedDecl struct {
	Field    string `msgpack:"field"`
	Resource string `msgpack:"resource"`
	IsList   bool   `msgpack:"is_list"`
}

func WriteRoutes(routes []RouteDeclaration) uint64 { return writePacked(routes) }

// SerialiseRouteTable is what the SDK's auto-generated get_routes export
// calls — routes aren't module-author-declared data the way schema/data
// migrations are; they're collected automatically by DefaultRouter as a
// side effect of GET/POST/etc. calls in init().
func SerialiseRouteTable() uint64 {
	return WriteRoutes(routeDeclarations(DefaultRouter.routes))
}
