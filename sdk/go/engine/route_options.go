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
	requestType  *TypeDesc
	responseType *TypeDesc

	model          string
	crudAction     string
	responseIsList bool
}

func newRouteConfig(opts ...RouteOption) routeConfig {
	c := routeConfig{
		auth:         AuthRequired,
		timeoutMs:    int(defaultTimeout / time.Millisecond),
		maxBodyBytes: defaultMaxBodyBytes,
	}
	for _, opt := range opts {
		opt.applyRoute(&c)
	}
	return c
}

// RouteOption configures a route registered via engine.GET/POST/PUT/PATCH/
// DELETE/WS/SSE (go-sdk-reference.md §2a "Route options").
type RouteOption interface {
	applyRoute(*routeConfig)
}

// ActionOption configures a route registered via engine.Action
// (go-sdk-reference.md §2a "Action options").
type ActionOption interface {
	applyAction(*actionConfig)
}

// CommonOption is an option accepted by both engine.Action and the raw
// route registrations.
type CommonOption interface {
	RouteOption
	ActionOption
}

type routeOptionFunc func(*routeConfig)

func (f routeOptionFunc) applyRoute(c *routeConfig) { f(c) }

type commonOptionFunc func(*routeConfig)

func (f commonOptionFunc) applyRoute(c *routeConfig)   { f(c) }
func (f commonOptionFunc) applyAction(c *actionConfig) { f(&c.routeConfig) }

// Auth sets the route's authentication requirement. Default: AuthRequired.
func Auth(mode AuthMode) RouteOption {
	return routeOptionFunc(func(c *routeConfig) { c.auth = mode })
}

// Requires declares the permissions a caller must hold, all of them
// (AND logic), before the handler is invoked.
func Requires(permissions ...string) CommonOption {
	return commonOptionFunc(func(c *routeConfig) { c.permissions = append(c.permissions, permissions...) })
}

// RateLimit overrides the engine-wide rate-limit default for this route.
func RateLimit(requests, windowSeconds int, scope RateLimitScope) CommonOption {
	return commonOptionFunc(func(c *routeConfig) {
		c.rateLimit = &RateLimitDecl{Requests: requests, WindowSeconds: windowSeconds, Scope: scope}
	})
}

// Timeout sets the route's request timeout. Default: 30s. Values above the
// 5-minute ceiling are clamped to it; negative values are clamped to 0.
func Timeout(d time.Duration) CommonOption {
	return commonOptionFunc(func(c *routeConfig) {
		switch {
		case d > maxTimeout:
			d = maxTimeout
		case d < 0:
			d = 0
		}
		c.timeoutMs = int(d / time.Millisecond)
	})
}

// MaxBody sets the maximum accepted request body size in bytes. Default:
// 32MB. Use 0 to reject any request body.
func MaxBody(bytes int) CommonOption {
	return commonOptionFunc(func(c *routeConfig) { c.maxBodyBytes = bytes })
}

// RawBody disables JSON auto-parsing for the route and makes the raw
// request body available via req.RawBody() — required for routes that
// verify a signature (e.g. inbound webhooks) over the exact bytes sent.
func RawBody() RouteOption {
	return routeOptionFunc(func(c *routeConfig) { c.rawBody = true })
}

// Streaming marks the route as returning a streaming response.
func Streaming() RouteOption {
	return routeOptionFunc(func(c *routeConfig) { c.streaming = true })
}

// Embeds declares an additional model type present in the route's
// response, so its fields also receive field-level access control.
func Embeds(field, resource string, isList bool) CommonOption {
	return commonOptionFunc(func(c *routeConfig) {
		c.embedded = append(c.embedded, EmbeddedDecl{Field: field, Resource: resource, IsList: isList})
	})
}

// PathParam declares a path parameter's expected kind.
func PathParam(name string, kind ParamKind) RouteOption {
	return routeOptionFunc(func(c *routeConfig) {
		if c.pathParams == nil {
			c.pathParams = make(map[string]string)
		}
		c.pathParams[name] = string(kind)
	})
}

// Model binds a hand-written route to a model, for the rare route that is
// not an engine.Action but returns model-shaped fields. engine.List also
// marks the response as a list.
func Model(resource string, action CRUDAction) RouteOption {
	return routeOptionFunc(func(c *routeConfig) {
		c.model = resource
		c.crudAction = string(action)
		c.responseIsList = action == List
	})
}
