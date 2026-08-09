package route

import (
	"maps"
	"net/url"
	"slices"
	"strings"
)

type RouteTable struct {
	tree *node
}

type node struct {
	staticChildren map[string]*node
	paramChild     *node
	paramName      string
	handlers       map[string]*RouteEntry
}

type RouteEntry struct {
	ModuleName   string
	Manifest     RouteManifest
	PathTemplate string
}

type LookupResult int

const (
	RouteFound LookupResult = iota
	RouteNotFound
	RouteMethodNotAllowed
	RouteBadPath
)

func New() *RouteTable {
	return &RouteTable{
		tree: &node{
			staticChildren: make(map[string]*node),
			handlers:       make(map[string]*RouteEntry),
		},
	}
}

func (rt *RouteTable) Register(method, path string, entry *RouteEntry) {
	normalizedPath := normalizePath(path)

	var segments []string
	if len(normalizedPath) != 0 {
		segments = strings.Split(normalizedPath, "/")
	}

	current := rt.tree

	for _, segment := range segments {
		if segment[0] == '{' && segment[len(segment)-1] == '}' {
			if current.paramChild == nil {
				current.paramChild = &node{}
				current.paramChild.staticChildren = make(map[string]*node)
				current.paramChild.handlers = make(map[string]*RouteEntry)
				current.paramChild.paramName = segment[1 : len(segment)-1]
			}
			current = current.paramChild
		} else {
			if _, ok := current.staticChildren[segment]; !ok {
				current.staticChildren[segment] = &node{}
				current.staticChildren[segment].staticChildren = make(map[string]*node)
				current.staticChildren[segment].handlers = make(map[string]*RouteEntry)
			}
			current = current.staticChildren[segment]
		}
	}

	current.handlers[method] = entry
}

func (rt *RouteTable) Lookup(method, path string) (*RouteEntry, map[string]string, LookupResult, []string) {
	normalizedPath := normalizePath(path)

	var segments []string
	if len(normalizedPath) != 0 {
		segments = strings.Split(normalizedPath, "/")
	}

	for i, segment := range segments {
		if segment == "." || segment == ".." {
			return nil, nil, RouteBadPath, nil
		}
		decoded, err := url.PathUnescape(segment)
		if err != nil {
			return nil, nil, RouteBadPath, nil
		}
		segments[i] = decoded
	}

	current := rt.tree
	pathParams := make(map[string]string)

	for _, segment := range segments {
		if child, ok := current.staticChildren[segment]; ok {
			current = child
		} else if current.paramChild != nil {
			current = current.paramChild
			pathParams[current.paramName] = segment
		} else {
			return nil, nil, RouteNotFound, nil
		}
	}

	if handler, ok := current.handlers[method]; ok {
		return handler, pathParams, RouteFound, nil
	}

	if len(current.handlers) != 0 {
		allowedMethods := slices.Collect(maps.Keys(current.handlers))
		slices.Sort(allowedMethods)
		return nil, nil, RouteMethodNotAllowed, allowedMethods
	}

	return nil, nil, RouteNotFound, nil
}

func (rt *RouteTable) find(segments []string) *node {
	current := rt.tree

	for _, segment := range segments {
		if segment == "" {
			continue
		}

		if strings.HasPrefix(segment, "{") && strings.HasSuffix(segment, "}") {
			if current.paramChild == nil {
				return nil
			}
			current = current.paramChild
			continue
		}

		child, ok := current.staticChildren[segment]
		if !ok {
			return nil
		}
		current = child
	}

	return current
}

func (rt *RouteTable) Registered(method, path string) bool {
	normalizedPath := normalizePath(path)

	var segments []string
	if normalizedPath != "" {
		segments = strings.Split(normalizedPath, "/")
	}

	n := rt.find(segments)
	if n == nil {
		return false
	}

	_, registered := n.handlers[method]
	return registered
}

func normalizePath(path string) string {
	var b strings.Builder
	previousSlash := false

	for _, c := range path {
		if c == '/' {
			if previousSlash {
				continue
			}
			previousSlash = true
		} else {
			previousSlash = false
		}

		b.WriteRune(c)
	}

	normalized := b.String()
	normalized = strings.Trim(normalized, "/")
	return normalized
}
