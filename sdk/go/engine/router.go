package engine

import (
	"maps"
	"net/url"
	"slices"
	"strings"
)

type Handler func(*Request) *Response

type route struct {
	method    string
	segments  []string
	handler   Handler
	name      string
	scope     string
	websocket bool
	routeConfig
}

type Router struct {
	routes []route
}

func NewRouter() *Router {
	return &Router{}
}

var DefaultRouter = NewRouter()

func (r *Router) register(method, pattern string, h Handler, opts ...RouteOption) {
	r.routes = append(r.routes, route{
		method:      method,
		segments:    splitPattern(pattern),
		handler:     h,
		routeConfig: newRouteConfig(opts...),
	})
}

func (r *Router) registerWebsocket(method, pattern string, h Handler, opts ...RouteOption) {
	r.routes = append(r.routes, route{
		method:      method,
		segments:    splitPattern(pattern),
		handler:     h,
		websocket:   true,
		routeConfig: newRouteConfig(opts...),
	})
}

func (r *Router) registerAction(model, name string, requestType *TypeDesc, h Handler, opts ...ActionOption) {
	cfg := actionConfig{routeConfig: newRouteConfig()}
	for _, opt := range opts {
		opt.applyAction(&cfg)
	}
	cfg.model = model
	cfg.requestType = requestType
	cfg.crudAction = crudActionOf(name)
	cfg.responseIsList = cfg.crudAction == actionList

	r.routes = append(r.routes, route{
		method:      string(cfg.method),
		handler:     h,
		name:        name,
		scope:       string(cfg.scope),
		routeConfig: cfg.routeConfig,
	})
}

func splitPattern(pattern string) []string {
	return strings.Split(strings.Trim(pattern, "/"), "/")
}

func GET(pattern string, h Handler, opts ...RouteOption) {
	DefaultRouter.register("GET", pattern, h, opts...)
}
func POST(pattern string, h Handler, opts ...RouteOption) {
	DefaultRouter.register("POST", pattern, h, opts...)
}
func PUT(pattern string, h Handler, opts ...RouteOption) {
	DefaultRouter.register("PUT", pattern, h, opts...)
}
func PATCH(pattern string, h Handler, opts ...RouteOption) {
	DefaultRouter.register("PATCH", pattern, h, opts...)
}
func DELETE(pattern string, h Handler, opts ...RouteOption) {
	DefaultRouter.register("DELETE", pattern, h, opts...)
}

// WS registers a WebSocket-upgrade route.
func WS(pattern string, h Handler, opts ...RouteOption) {
	DefaultRouter.registerWebsocket("GET", pattern, h, opts...)
}

// SSE registers a server-sent-events route. Streaming() is implied, the
// same way WS implies Websocket — an SSE route is a long-lived streaming
// response by definition, not an opt-in a module author declares separately.
func SSE(pattern string, h Handler, opts ...RouteOption) {
	opts = append([]RouteOption{Streaming()}, opts...)
	DefaultRouter.register("GET", pattern, h, opts...)
}

func (r *Router) Handle(req *Request) *Response {
	if req.Model != "" && req.Action != "" {
		return r.handleAction(req)
	}

	reqSegments, ok := splitEscapedPath(req.Path)
	if !ok {
		return notFound()
	}

	rt, params, ok := r.lookup(req.Method, reqSegments)
	if !ok {
		return notFound()
	}

	if req.PathParams == nil {
		req.PathParams = params
	} else {
		maps.Copy(req.PathParams, params)
	}

	return rt.handler(req)
}

// lookup selects the route the engine's route table resolves for the
// same path: at each segment a static match beats a parameter match,
// independent of registration order, and the method is compared only
// once the whole path is consumed.
func (r *Router) lookup(method string, reqSegments []string) (route, map[string]string, bool) {
	candidates := make([]route, 0, len(r.routes))
	for _, rt := range r.routes {
		if !rt.isAction() && len(rt.segments) == len(reqSegments) {
			candidates = append(candidates, rt)
		}
	}

	for i, segment := range reqSegments {
		hasStatic := slices.ContainsFunc(candidates, func(rt route) bool {
			return matchesStatic(rt.segments[i], segment)
		})
		candidates = slices.DeleteFunc(candidates, func(rt route) bool {
			if hasStatic {
				return !matchesStatic(rt.segments[i], segment)
			}
			return !isParamSegment(rt.segments[i])
		})
	}

	for _, rt := range candidates {
		if rt.method != method {
			continue
		}
		if params, ok := matchSegments(rt.segments, reqSegments); ok {
			return rt, params, true
		}
	}
	return route{}, nil, false
}

func isParamSegment(seg string) bool {
	return strings.HasPrefix(seg, "{") && strings.HasSuffix(seg, "}")
}

func matchesStatic(patternSegment, segment string) bool {
	return !isParamSegment(patternSegment) && patternSegment == segment
}

// handleAction dispatches a request the engine matched to an action
// route: the engine already extracted the path parameters, so only the
// route's (model, action) identity is compared.
func (r *Router) handleAction(req *Request) *Response {
	for _, rt := range r.routes {
		if rt.isAction() && rt.model == req.Model && rt.name == req.Action {
			return rt.handler(req)
		}
	}
	return notFound()
}

func (rt route) isAction() bool {
	return rt.name != ""
}

// splitEscapedPath splits a percent-encoded request path on literal "/"
// and only then decodes each segment, the order the engine's own route
// lookup uses, so an encoded %2F inside a parameter value stays part of
// that one segment. ok is false for a malformed escape.
func splitEscapedPath(path string) (segments []string, ok bool) {
	segments = strings.Split(strings.Trim(path, "/"), "/")
	for i, segment := range segments {
		decoded, err := url.PathUnescape(segment)
		if err != nil {
			return nil, false
		}
		segments[i] = decoded
	}
	return segments, true
}

// matchSegments compares a registered route's path segments against an
// incoming request's path segments, extracting any {name} placeholders.
func matchSegments(pattern, path []string) (map[string]string, bool) {
	if len(pattern) != len(path) {
		return nil, false
	}

	params := map[string]string{}
	for i, seg := range pattern {
		if isParamSegment(seg) {
			params[seg[1:len(seg)-1]] = path[i]
			continue
		}
		if seg != path[i] {
			return nil, false
		}
	}
	return params, true
}

func routeDeclarations(routes []route) []RouteDeclaration {
	decls := make([]RouteDeclaration, 0, len(routes))
	for _, r := range routes {
		path := ""
		if !r.isAction() {
			path = "/" + strings.Join(r.segments, "/")
		}
		decls = append(decls, RouteDeclaration{
			Method:         r.method,
			Path:           path,
			Auth:           string(r.auth),
			Permissions:    r.permissions,
			RateLimit:      r.rateLimit,
			MaxBodyBytes:   r.maxBodyBytes,
			TimeoutMs:      r.timeoutMs,
			Streaming:      r.streaming,
			Websocket:      r.websocket,
			RawBody:        r.rawBody,
			Model:          r.model,
			Name:           r.name,
			Scope:          r.scope,
			CRUDAction:     r.crudAction,
			ResponseIsList: r.responseIsList,
			Embedded:       r.embedded,
			PathParams:     r.pathParams,
			RequestType:    r.requestType,
			ResponseType:   r.responseType,
		})
	}
	return decls
}
