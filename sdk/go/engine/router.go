package engine

import (
	"maps"
	"strings"
)

type Handler func(*Request) *Response

type route struct {
	method     string
	segments   []string
	handler    Handler
	model      string
	name       string
	crudAction string
	websocket  bool
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

func (r *Router) registerAction(method, pattern, model, name, crudAction string, h Handler) {
	r.routes = append(r.routes, route{
		method:     method,
		segments:   splitPattern(pattern),
		handler:    h,
		model:      model,
		name:       name,
		crudAction: crudAction,
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

// SSE registers a server-sent-events route.
func SSE(pattern string, h Handler, opts ...RouteOption) {
	DefaultRouter.register("GET", pattern, h, opts...)
}

func (r *Router) Handle(req *Request) *Response {
	reqSegments := strings.Split(strings.Trim(req.Path, "/"), "/")

	for _, rt := range r.routes {
		if rt.method != req.Method {
			continue
		}

		params, ok := matchSegments(rt.segments, reqSegments)
		if !ok {
			continue
		}

		if req.PathParams == nil {
			req.PathParams = params
		} else {
			maps.Copy(req.PathParams, params)
		}

		return rt.handler(req)
	}

	return notFound()
}

// matchSegments compares a registered route's path segments against an
// incoming request's path segments, extracting any {name} placeholders.
func matchSegments(pattern, path []string) (map[string]string, bool) {
	if len(pattern) != len(path) {
		return nil, false
	}

	params := map[string]string{}
	for i, seg := range pattern {
		if strings.HasPrefix(seg, "{") && strings.HasSuffix(seg, "}") {
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
		decls = append(decls, RouteDeclaration{
			Method:       r.method,
			Path:         "/" + strings.Join(r.segments, "/"),
			Auth:         string(r.auth),
			Permissions:  r.permissions,
			RateLimit:    r.rateLimit,
			MaxBodyBytes: r.maxBodyBytes,
			TimeoutMs:    r.timeoutMs,
			Streaming:    r.streaming,
			Websocket:    r.websocket,
			RawBody:      r.rawBody,
			Model:        r.model,
			Name:         r.name,
			CRUDAction:   r.crudAction,
			Embedded:     r.embedded,
			PathParams:   r.pathParams,
		})
	}
	return decls
}
