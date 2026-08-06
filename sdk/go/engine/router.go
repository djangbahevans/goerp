package engine

import (
	"maps"
	"strings"
)

type Handler func(*Request) *Response

type route struct {
	method   string
	segments []string
	handler  Handler
}

type Router struct {
	routes []route
}

func NewRouter() *Router {
	return &Router{}
}

var DefaultRouter = NewRouter()

func (r *Router) register(method, pattern string, h Handler) {
	r.routes = append(r.routes, route{
		method:   method,
		segments: strings.Split(strings.Trim(pattern, "/"), "/"),
		handler:  h,
	})
}

func GET(pattern string, h Handler)    { DefaultRouter.register("GET", pattern, h) }
func POST(pattern string, h Handler)   { DefaultRouter.register("POST", pattern, h) }
func PUT(pattern string, h Handler)    { DefaultRouter.register("PUT", pattern, h) }
func PATCH(pattern string, h Handler)  { DefaultRouter.register("PATCH", pattern, h) }
func DELETE(pattern string, h Handler) { DefaultRouter.register("DELETE", pattern, h) }

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
