package engine

import "time"

// AuthMode selects a route's authentication requirement (engine.Auth).
type AuthMode string

const (
	AuthRequired AuthMode = "required" // valid session required (default)
	AuthOptional AuthMode = "optional" // session used if present, not required
	AuthNone     AuthMode = "none"     // no auth — for inbound webhook routes
)

// ParamKind selects the validation rule for a declared path parameter
// (engine.PathParam).
type ParamKind string

const (
	UUIDParam ParamKind = "uuid"
	SlugParam ParamKind = "slug"
	IntParam  ParamKind = "int"
)

const (
	defaultTimeout      = 30 * time.Second
	maxTimeout          = 5 * time.Minute
	defaultMaxBodyBytes = 32 * 1024 * 1024 // 32MB
)

// routeConfig accumulates a route's RouteOption-derived declaration fields.
// Defaults are seeded by newRouteConfig before options are applied, so an
// explicit option always overrides a default, and MaxBody(0) — a
// deliberate "no body" declaration — is distinguishable from MaxBody never
// being called at all (both leave a real value in maxBodyBytes, but only
// the latter is the seeded default).
type routeConfig struct {
	auth         AuthMode
	permissions  []string
	rateLimit    *RateLimitDecl
	timeoutMs    int
	maxBodyBytes int
	rawBody      bool
	streaming    bool
	embedded     []EmbeddedDecl
	pathParams   map[string]string
	queryParams  map[string]QueryParamDecl
}

func newRouteConfig(opts ...RouteOption) routeConfig {
	c := routeConfig{
		auth:         AuthRequired,
		timeoutMs:    int(defaultTimeout / time.Millisecond),
		maxBodyBytes: defaultMaxBodyBytes,
	}
	for _, opt := range opts {
		opt(&c)
	}
	return c
}

// RouteOption configures a route registered via engine.GET/POST/PUT/PATCH/
// DELETE/WS/SSE (go-sdk-reference.md §2a "Route options").
type RouteOption func(*routeConfig)

// Auth sets the route's authentication requirement. Default: AuthRequired.
func Auth(mode AuthMode) RouteOption {
	return func(c *routeConfig) { c.auth = mode }
}

// Requires declares the permissions a caller must hold, all of them
// (AND logic), before the handler is invoked.
func Requires(permissions ...string) RouteOption {
	return func(c *routeConfig) { c.permissions = append(c.permissions, permissions...) }
}

// RateLimit overrides the engine-wide rate-limit default for this route.
func RateLimit(requests, windowSeconds int, scope RateLimitScope) RouteOption {
	return func(c *routeConfig) {
		c.rateLimit = &RateLimitDecl{Requests: requests, WindowSeconds: windowSeconds, Scope: scope}
	}
}

// Timeout sets the route's request timeout. Default: 30s. Values above the
// 5-minute ceiling are clamped to it; negative values are clamped to 0.
func Timeout(d time.Duration) RouteOption {
	return func(c *routeConfig) {
		switch {
		case d > maxTimeout:
			d = maxTimeout
		case d < 0:
			d = 0
		}
		c.timeoutMs = int(d / time.Millisecond)
	}
}

// MaxBody sets the maximum accepted request body size in bytes. Default:
// 32MB. Use 0 to reject any request body.
func MaxBody(bytes int) RouteOption {
	return func(c *routeConfig) { c.maxBodyBytes = bytes }
}

// RawBody disables JSON auto-parsing for the route and makes the raw
// request body available via req.RawBody() — required for routes that
// verify a signature (e.g. inbound webhooks) over the exact bytes sent.
func RawBody() RouteOption {
	return func(c *routeConfig) { c.rawBody = true }
}

// Streaming marks the route as returning a streaming response.
func Streaming() RouteOption {
	return func(c *routeConfig) { c.streaming = true }
}

// Embeds declares an additional model type present in the route's
// response, so its fields also receive field-level access control.
func Embeds(field, resource string, isList bool) RouteOption {
	return func(c *routeConfig) {
		c.embedded = append(c.embedded, EmbeddedDecl{Field: field, Resource: resource, IsList: isList})
	}
}

// PathParam declares a path parameter's expected kind.
func PathParam(name string, kind ParamKind) RouteOption {
	return func(c *routeConfig) {
		if c.pathParams == nil {
			c.pathParams = make(map[string]string)
		}
		c.pathParams[name] = string(kind)
	}
}

// QueryParamType selects a declared query parameter's value type
// (engine.QueryParam) — descriptive only, for /_meta/schema and codegen;
// see QueryParamDecl's doc comment.
type QueryParamType string

const (
	QueryString  QueryParamType = "string"
	QueryInteger QueryParamType = "integer"
	QueryBoolean QueryParamType = "boolean"
)

// QueryParamSetting further describes a query parameter declared via
// QueryParam — QueryEnum, QueryDefault, or QueryMax below.
type QueryParamSetting func(*QueryParamDecl)

// QueryEnum restricts a query parameter to one of values.
func QueryEnum(values ...string) QueryParamSetting {
	return func(d *QueryParamDecl) { d.Enum = values }
}

// QueryDefault documents a query parameter's default value when the
// caller omits it.
func QueryDefault(v any) QueryParamSetting {
	return func(d *QueryParamDecl) { d.Default = v }
}

// QueryMax documents a query parameter's maximum accepted value
// (QueryInteger) or length (QueryString).
func QueryMax(n int) QueryParamSetting {
	return func(d *QueryParamDecl) { d.Max = &n }
}

// QueryParam declares a query-string parameter a route accepts —
// descriptive metadata surfaced via /_meta/schema and goerp codegen's
// generated client types (go-sdk-reference.md §2a "Route options"). It
// does not affect request handling: use req.QueryParam and friends to
// actually read the value.
func QueryParam(name string, typ QueryParamType, settings ...QueryParamSetting) RouteOption {
	return func(c *routeConfig) {
		if c.queryParams == nil {
			c.queryParams = make(map[string]QueryParamDecl)
		}
		d := QueryParamDecl{Type: string(typ)}
		for _, s := range settings {
			s(&d)
		}
		c.queryParams[name] = d
	}
}
